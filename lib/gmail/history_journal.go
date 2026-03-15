package gmail

import (
	"database/sql"
	"fmt"
	"strings"
	"time"
)

const historyJournalFlagExistsAfter = 1
const labelsJoinSep = "\x1f"

type historyJournalRow struct {
	HistoryID uint64
	MessageID string
	Op        int32
	Flags     int
	Labels    []string
}

func encodeJournalLabels(labels []string) *string {
	if len(labels) == 0 {
		return nil
	}
	n := normalizeLabels(labels)
	s := strings.Join(n, labelsJoinSep)
	return &s
}

func decodeJournalLabels(raw sql.NullString) []string {
	if !raw.Valid || raw.String == "" {
		return nil
	}
	return normalizeLabels(strings.Split(raw.String, labelsJoinSep))
}

func appendHistoryJournal(tx *sql.Tx, row historyJournalRow) error {
	labels := encodeJournalLabels(row.Labels)
	_, err := tx.Exec(`INSERT INTO gmail_history_journal(historyId, messageId, op, flags, labels, appliedAtMs)
		VALUES(?, ?, ?, ?, ?, ?)
		ON CONFLICT(historyId, messageId, op) DO UPDATE SET flags=excluded.flags, labels=excluded.labels, appliedAtMs=excluded.appliedAtMs`,
		row.HistoryID, row.MessageID, row.Op, row.Flags, labels, time.Now().UnixMilli())
	return err
}

func loadHistoryJournalFrom(db *sql.DB, startHistoryID uint64) ([]historyJournalRow, error) {
	rows, err := db.Query(`SELECT historyId, messageId, op, flags, labels FROM gmail_history_journal WHERE historyId >= ? ORDER BY historyId ASC, messageId ASC, op ASC`, startHistoryID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make([]historyJournalRow, 0, 256)
	for rows.Next() {
		var r historyJournalRow
		var labels sql.NullString
		if err := rows.Scan(&r.HistoryID, &r.MessageID, &r.Op, &r.Flags, &labels); err != nil {
			return nil, err
		}
		r.Labels = decodeJournalLabels(labels)
		out = append(out, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return out, nil
}

func validateReplayCoverage(rows []historyJournalRow, startHistoryID uint64, committedHistoryID uint64) error {
	if len(rows) == 0 {
		return fmt.Errorf("no journal rows at/after startHistoryId=%d", startHistoryID)
	}
	maxH := rows[len(rows)-1].HistoryID
	_ = startHistoryID
	if committedHistoryID > 0 && maxH < committedHistoryID {
		return fmt.Errorf("journal gap: latest journal historyId=%d < committedHistoryId=%d", maxH, committedHistoryID)
	}
	return nil
}
