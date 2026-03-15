package gmail

import (
	"database/sql"
	"fmt"
	"log"
	"strconv"
	"sync"
	"time"

	"google.golang.org/api/googleapi"
)

func (g *Gmail) SyncHistoryWithDB(db *sql.DB) error {
	if err := ensureListPagesSchema(db); err != nil {
		return err
	}

	cursor, pageToken, err := getHistoryResumeState(db)
	if err != nil {
		return err
	}
	if g.startHistoryIdOverride > 0 {
		cursor = g.startHistoryIdOverride
		pageToken = ""
		if err := setHistoryProgress(db, cursor, ""); err != nil {
			return err
		}
	}
	if cursor == 0 {
		cursor, err = g.bootstrapHistoryCursor(db)
		if err != nil {
			return err
		}
		if cursor == 0 {
			log.Printf("history: skipped (no bootstrap history cursor available)")
			return nil
		}
		if err := setHistoryProgress(db, cursor, ""); err != nil {
			return err
		}
	}

	log.Printf("history: start cursor=%d resume_page=%t", cursor, pageToken != "")
	start := time.Now()
	lastPerf := time.Now()
	maxSeen := cursor
	var added, deleted, labeled, events int
	var labelsMissingFile, labelsSelfHealed, labelsApplyFailed int
	labelsRefreshedOnMiss := false
	var labelsRefreshMu sync.Mutex

	for {
		pageJournalRows := make([]historyJournalRow, 0, 128)
		r, err := g.svc.GetHistory(cursor, g.labelId, pageToken)
		if err != nil {
			if e, ok := err.(*googleapi.Error); ok && e.Code == 404 {
				log.Printf("history: startHistoryId=%d expired; attempting local journal replay", cursor)
				if replayErr := g.replayFromLocalJournal(db, cursor); replayErr == nil {
					log.Printf("history: local journal replay complete; resuming normal sync")
					return nil
				} else {
					log.Printf("history: local journal replay unavailable (%v); falling back to Listing + Downloading-Archived", replayErr)
				}
				if err := clearHistoryState(db); err != nil {
					return err
				}
				if err := g.SyncListPagesWithDB(db); err != nil {
					return err
				}
				return g.SyncListedMessagesWithDB(db)
			}
			return err
		}

		for _, h := range r.History {
			events++
			if h.Id > maxSeen {
				maxSeen = h.Id
			}
			for _, a := range h.MessagesAdded {
				if a.Message == nil || a.Message.Id == "" {
					continue
				}
				didAdd, finalLabels, err := g.downloadAndWriteHistoryMessage(db, a.Message.Id, &labelsRefreshedOnMiss, &labelsRefreshMu)
				if err != nil {
					return err
				}
				if didAdd {
					added++
				}
				if didAdd {
					pageJournalRows = append(pageJournalRows, historyJournalRow{HistoryID: h.Id, MessageID: a.Message.Id, Op: ADD, Flags: historyJournalFlagExistsAfter, Labels: finalLabels})
				}
			}
			for _, d := range h.MessagesDeleted {
				if d.Message == nil || d.Message.Id == "" {
					continue
				}
				if err := g.deleteMessageByID(d.Message.Id); err == nil {
					deleted++
					pageJournalRows = append(pageJournalRows, historyJournalRow{HistoryID: h.Id, MessageID: d.Message.Id, Op: DELETE, Flags: 0})
				}
			}
			for _, l := range h.LabelsAdded {
				if l.Message == nil || l.Message.Id == "" {
					continue
				}
				applied, missingFile, selfHealed, nextLabels, err := g.applyHistoryLabelDelta(db, l.Message.Id, l.LabelIds, nil, &labelsRefreshedOnMiss, &labelsRefreshMu)
				if err != nil {
					labelsApplyFailed++
					log.Printf("history: label-add apply failed message=%s err=%v", l.Message.Id, err)
					continue
				}
				if applied {
					labeled++
					pageJournalRows = append(pageJournalRows, historyJournalRow{HistoryID: h.Id, MessageID: l.Message.Id, Op: WRITE_LABELS, Flags: historyJournalFlagExistsAfter, Labels: nextLabels})
				}
				if missingFile {
					labelsMissingFile++
				}
				if selfHealed {
					labelsSelfHealed++
				}
			}
			for _, l := range h.LabelsRemoved {
				if l.Message == nil || l.Message.Id == "" {
					continue
				}
				applied, missingFile, selfHealed, nextLabels, err := g.applyHistoryLabelDelta(db, l.Message.Id, nil, l.LabelIds, &labelsRefreshedOnMiss, &labelsRefreshMu)
				if err != nil {
					labelsApplyFailed++
					log.Printf("history: label-remove apply failed message=%s err=%v", l.Message.Id, err)
					continue
				}
				if applied {
					labeled++
					pageJournalRows = append(pageJournalRows, historyJournalRow{HistoryID: h.Id, MessageID: l.Message.Id, Op: WRITE_LABELS, Flags: historyJournalFlagExistsAfter, Labels: nextLabels})
				}
				if missingFile {
					labelsMissingFile++
				}
				if selfHealed {
					labelsSelfHealed++
				}
			}
		}

		pageToken = r.NextPageToken
		if err := setHistoryProgressWithJournal(db, maxSeen, pageToken, pageJournalRows); err != nil {
			return err
		}

		if time.Since(lastPerf) > 2*time.Second {
			elapsed := time.Since(start).Seconds()
			if elapsed <= 0 {
				elapsed = 0.001
			}
			log.Printf("history: perf events=%d added=%d deleted=%d labels_applied=%d labels_missing_file=%d labels_self_healed=%d labels_apply_failed=%d rate=%.2f ev/s cursor=%d page=%t elapsed=%.1fs",
				events, added, deleted, labeled, labelsMissingFile, labelsSelfHealed, labelsApplyFailed, float64(events)/elapsed, maxSeen, pageToken != "", elapsed)
			lastPerf = time.Now()
		}

		if pageToken == "" {
			break
		}
	}

	if err := commitHistoryCursor(db, maxSeen); err != nil {
		return err
	}
	elapsed := time.Since(start).Seconds()
	if elapsed <= 0 {
		elapsed = 0.001
	}
	log.Printf("history: complete cursor=%d events=%d added=%d deleted=%d labels_applied=%d labels_missing_file=%d labels_self_healed=%d labels_apply_failed=%d elapsed=%.1fs rate=%.2f ev/s",
		maxSeen, events, added, deleted, labeled, labelsMissingFile, labelsSelfHealed, labelsApplyFailed, elapsed, float64(events)/elapsed)
	return nil
}

func getHistoryResumeState(db *sql.DB) (uint64, string, error) {
	if v, ok, err := getSyncState(db, syncStateHistoryCursorProgress); err != nil {
		return 0, "", err
	} else if ok && v != "" {
		u, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return 0, "", err
		}
		p, _, err := getSyncState(db, syncStateHistoryPageToken)
		return u, p, err
	}
	if v, ok, err := getSyncState(db, syncStateHistoryCursorCommitted); err != nil {
		return 0, "", err
	} else if ok && v != "" {
		u, err := strconv.ParseUint(v, 10, 64)
		if err != nil {
			return 0, "", err
		}
		return u, "", nil
	}
	return 0, "", nil
}

func setHistoryProgress(db *sql.DB, cursor uint64, pageToken string) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if err := setSyncState(tx, syncStateHistoryCursorProgress, strconv.FormatUint(cursor, 10)); err != nil {
		tx.Rollback()
		return err
	}
	if err := setSyncState(tx, syncStateHistoryPageToken, pageToken); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func commitHistoryCursor(db *sql.DB, cursor uint64) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	if err := setSyncState(tx, syncStateHistoryCursorCommitted, strconv.FormatUint(cursor, 10)); err != nil {
		tx.Rollback()
		return err
	}
	for _, k := range []string{syncStateHistoryCursorProgress, syncStateHistoryPageToken} {
		if _, err := tx.Exec(`DELETE FROM sync_state WHERE key = ?`, k); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func clearHistoryState(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	for _, k := range []string{syncStateHistoryCursorCommitted, syncStateHistoryCursorProgress, syncStateHistoryPageToken} {
		if _, err := tx.Exec(`DELETE FROM sync_state WHERE key = ?`, k); err != nil {
			tx.Rollback()
			return err
		}
	}
	return tx.Commit()
}

func (g *Gmail) bootstrapHistoryCursor(db *sql.DB) (uint64, error) {
	msg, ok, err := firstListedMessage(db)
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, nil
	}
	m, err := g.svc.GetMetadata(msg.MessageID)
	if err != nil {
		return 0, fmt.Errorf("history bootstrap failed: cannot fetch metadata for first listed message id=%s (responseId=%d): %w", msg.MessageID, msg.ResponseID, err)
	}
	if m == nil || m.HistoryId == 0 {
		return 0, fmt.Errorf("history bootstrap failed: first listed message id=%s (responseId=%d) has no usable historyId", msg.MessageID, msg.ResponseID)
	}
	log.Printf("history: bootstrapped cursor=%d from first listed message=%s responseId=%d", m.HistoryId, msg.MessageID, msg.ResponseID)
	return m.HistoryId, nil
}

func (g *Gmail) downloadAndWriteHistoryMessage(db *sql.DB, id string, labelsRefreshedOnMiss *bool, labelsRefreshMu *sync.Mutex) (bool, []string, error) {
	k := messageMaildirKey(id)
	if _, err := g.dir.GetFile(k); err == nil {
		return false, nil, nil
	}
	op := g.handleNewMsg(id)
	if op.Error != nil {
		return false, nil, op.Error
	}
	if op.Operation == NONE {
		return false, nil, nil
	}
	if op.Operation != ADD {
		return false, nil, nil
	}
	mapped, err := g.resolveArchivedLabels(db, op.Labels, labelsRefreshedOnMiss, labelsRefreshMu)
	if err != nil {
		return false, nil, err
	}
	op.Msg.Header[labelsHeader] = mapped
	if _, err := g.dir.DeliverWithKey(op.Msg, k); err != nil {
		return false, nil, err
	}
	return true, mapped, nil
}

func (g *Gmail) deleteMessageByID(id string) error {
	k := messageMaildirKey(id)
	if err := g.dir.Delete(k); err == nil {
		return nil
	}
	return g.writeDel(id)
}

func (g *Gmail) applyHistoryLabelDelta(db *sql.DB, id string, add, remove []string, labelsRefreshedOnMiss *bool, labelsRefreshMu *sync.Mutex) (applied bool, missingFile bool, selfHealed bool, nextLabels []string, err error) {
	k := messageMaildirKey(id)
	msg, c, err := g.getMaildirMessage(k)
	if err != nil {
		missingFile = true
		log.Printf("history: missing local file for label delta message=%s, attempting self-heal", id)
		didAdd, _, healErr := g.downloadAndWriteHistoryMessage(db, id, labelsRefreshedOnMiss, labelsRefreshMu)
		if healErr != nil {
			return false, true, false, nil, healErr
		}
		if !didAdd {
			return false, true, false, nil, nil
		}
		selfHealed = true
		msg, c, err = g.getMaildirMessage(k)
		if err != nil {
			return false, true, true, nil, nil
		}
	}
	defer c.Close()
	currentLabels, err := readLabelsFromMaildirMessage(msg)
	if err != nil {
		return false, missingFile, false, nil, err
	}
	addedLabels, err := g.resolveArchivedLabels(db, add, labelsRefreshedOnMiss, labelsRefreshMu)
	if err != nil {
		return false, missingFile, false, nil, err
	}
	removedLabels, err := g.resolveArchivedLabels(db, remove, labelsRefreshedOnMiss, labelsRefreshMu)
	if err != nil {
		return false, missingFile, false, nil, err
	}

	nextLabels = applyHeaderLabelDelta(currentLabels, addedLabels, removedLabels)
	msg.Header[labelsHeader] = nextLabels
	if err := g.dir.Delete(k); err != nil {
		return false, missingFile, false, nil, err
	}
	if _, err := g.dir.DeliverWithKey(msg, k); err != nil {
		return false, missingFile, false, nil, err
	}
	return true, missingFile, selfHealed, nextLabels, nil
}

func setHistoryProgressWithJournal(db *sql.DB, cursor uint64, pageToken string, rows []historyJournalRow) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	for _, row := range rows {
		if err := appendHistoryJournal(tx, row); err != nil {
			tx.Rollback()
			return err
		}
	}
	if err := setSyncState(tx, syncStateHistoryCursorProgress, strconv.FormatUint(cursor, 10)); err != nil {
		tx.Rollback()
		return err
	}
	if err := setSyncState(tx, syncStateHistoryPageToken, pageToken); err != nil {
		tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (g *Gmail) replayFromLocalJournal(db *sql.DB, startHistoryID uint64) error {
	rows, err := loadHistoryJournalFrom(db, startHistoryID)
	if err != nil {
		return err
	}
	committedRaw, ok, err := getSyncState(db, syncStateHistoryCursorCommitted)
	if err != nil {
		return err
	}
	committed := uint64(0)
	if ok && committedRaw != "" {
		committed, err = strconv.ParseUint(committedRaw, 10, 64)
		if err != nil {
			return err
		}
	}
	if err := validateReplayCoverage(rows, startHistoryID, committed); err != nil {
		return err
	}
	maxSeen := startHistoryID
	for _, row := range rows {
		if row.HistoryID > maxSeen {
			maxSeen = row.HistoryID
		}
		if err := g.applyJournalRowConvergence(row); err != nil {
			return err
		}
	}
	return commitHistoryCursor(db, maxSeen)
}

func (g *Gmail) applyJournalRowConvergence(row historyJournalRow) error {
	k := messageMaildirKey(row.MessageID)
	existsAfter := (row.Flags & historyJournalFlagExistsAfter) != 0
	if !existsAfter {
		if err := g.dir.Delete(k); err != nil {
			return g.writeDel(row.MessageID)
		}
		return nil
	}
	msg, c, err := g.getMaildirMessage(k)
	if err != nil {
		body, berr := g.getBody(row.MessageID)
		if berr != nil || body == nil {
			return berr
		}
		body.Header[labelsHeader] = normalizeLabels(row.Labels)
		if _, derr := g.dir.DeliverWithKey(body, k); derr != nil {
			return derr
		}
		msg, c, err = g.getMaildirMessage(k)
		if err != nil {
			return err
		}
	}
	defer c.Close()
	msg.Header[labelsHeader] = normalizeLabels(row.Labels)
	if err := g.dir.Delete(k); err != nil {
		return err
	}
	_, err = g.dir.DeliverWithKey(msg, k)
	return err
}

func applyHeaderLabelDelta(current, add, remove []string) []string {
	next := make(map[string]struct{}, len(current)+len(add))
	for _, l := range current {
		next[l] = struct{}{}
	}
	for _, l := range add {
		next[l] = struct{}{}
	}
	for _, l := range remove {
		delete(next, l)
	}
	out := make([]string, 0, len(next))
	for l := range next {
		out = append(out, l)
	}
	return normalizeLabels(out)
}
