package gmail

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gmailapi "google.golang.org/api/gmail/v1"
	"google.golang.org/api/googleapi"
)

func TestSyncHistoryWithDBCommitsCursor(t *testing.T) {
	g, svc, dir := getTestClient()
	db := openTestDB(t, filepath.Join(dir, "history.db"))
	defer db.Close()
	if err := ensureListPagesSchema(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sync_state(key,value,updatedAtMs) VALUES(?,?,1)`, syncStateHistoryCursorCommitted, "100"); err != nil {
		t.Fatal(err)
	}

	svc.History[""] = &gmailapi.ListHistoryResponse{
		History: []*gmailapi.History{{Id: 110}},
	}

	if err := g.SyncHistoryWithDB(db); err != nil {
		t.Fatal(err)
	}
	v, ok, err := getSyncState(db, syncStateHistoryCursorCommitted)
	if err != nil || !ok {
		t.Fatalf("committed cursor missing: %v %v", ok, err)
	}
	if v != "110" {
		t.Fatalf("committed cursor=%s expected 110", v)
	}
}

func TestSyncHistoryWithDBBootstrapsCursor(t *testing.T) {
	g, svc, dir := getTestClient()
	db := openTestDB(t, filepath.Join(dir, "history_bootstrap.db"))
	defer db.Close()
	if err := ensureListPagesSchema(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO gmail_users_messages_list_requests(pageToken, requestedAtMs, nextPageToken, resultSizeEstimate, rawJson) VALUES('',1,'',0,'{}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO gmail_users_messages_list_responses(id, requestId, nextPageToken, resultSizeEstimate, receivedAtMs, rawJson) VALUES(1,1,'',0,1,'{}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO gmail_users_messages_list_responses(id, requestId, nextPageToken, resultSizeEstimate, receivedAtMs, rawJson) VALUES(2,1,'',0,1,'{}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO gmail_users_messages_list_response_messages(responseId, id, threadId, rawJson) VALUES(1, 'newer', 't1', '{}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO gmail_users_messages_list_response_messages(responseId, id, threadId, rawJson) VALUES(2, 'older', 't2', '{}')`); err != nil {
		t.Fatal(err)
	}
	svc.Metadata["newer"] = &gmailapi.Message{Id: "newer", HistoryId: 200}
	svc.Metadata["older"] = &gmailapi.Message{Id: "older", HistoryId: 100}
	svc.History[""] = &gmailapi.ListHistoryResponse{}

	if err := g.SyncHistoryWithDB(db); err != nil {
		t.Fatal(err)
	}
	v, ok, err := getSyncState(db, syncStateHistoryCursorCommitted)
	if err != nil || !ok {
		t.Fatalf("committed cursor missing: %v %v", ok, err)
	}
	if v != "200" {
		t.Fatalf("committed cursor=%s expected 200", v)
	}
}

func TestHistoryLabelDeltaUsesMaildirStateWithoutSQLiteRows(t *testing.T) {
	g, svc, dir := getTestClient()
	db := openTestDB(t, filepath.Join(dir, "history_labels.db"))
	defer db.Close()
	if err := ensureListPagesSchema(db); err != nil {
		t.Fatal(err)
	}
	seedListRows(t, db, []listedMessage{{MessageID: "m1"}})
	if _, err := db.Exec(`INSERT INTO gmail_labels(id, name, type, updatedAtMs) VALUES('Label_1','One','user',1),('Label_2','Two','user',1)`); err != nil {
		t.Fatal(err)
	}
	raw := base64.URLEncoding.EncodeToString([]byte("From: a@b\nTo: c@d\nSubject: hi\n\nbody"))
	svc.Msgs["m1"] = raw
	svc.Metadata["m1"] = &gmailapi.Message{Id: "m1", LabelIds: []string{"Label_1"}}
	if err := g.SyncListedMessagesWithDB(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sync_state(key,value,updatedAtMs) VALUES(?,?,1)`, syncStateHistoryCursorCommitted, "100"); err != nil {
		t.Fatal(err)
	}
	svc.History[""] = &gmailapi.ListHistoryResponse{History: []*gmailapi.History{{Id: 105, LabelsAdded: []*gmailapi.HistoryLabelAdded{{Message: &gmailapi.Message{Id: "m1"}, LabelIds: []string{"Label_2"}}}}}}

	if err := g.SyncHistoryWithDB(db); err != nil {
		t.Fatal(err)
	}
	fn, err := g.dir.GetFile(messageMaildirKey("m1"))
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(fn)
	if err != nil {
		t.Fatal(err)
	}
	txt := string(b)
	if !strings.Contains(txt, "X-Keywords: One") || !strings.Contains(txt, "X-Keywords: Two") {
		t.Fatalf("expected mapped labels One+Two in headers")
	}
}

func TestSyncHistoryWithDBWritesReplayJournalRows(t *testing.T) {
	g, svc, dir := getTestClient()
	db := openTestDB(t, filepath.Join(dir, "history_journal.db"))
	defer db.Close()
	if err := ensureListPagesSchema(db); err != nil {
		t.Fatal(err)
	}
	seedListRows(t, db, []listedMessage{{MessageID: "m1"}})
	if _, err := db.Exec(`INSERT INTO gmail_labels(id, name, type, updatedAtMs) VALUES('Label_1','One','user',1),('Label_2','Two','user',1)`); err != nil {
		t.Fatal(err)
	}
	raw := base64.URLEncoding.EncodeToString([]byte("From: a@b\nTo: c@d\nSubject: hi\n\nbody"))
	svc.Msgs["m1"] = raw
	svc.Metadata["m1"] = &gmailapi.Message{Id: "m1", LabelIds: []string{"Label_1"}}
	if err := g.SyncListedMessagesWithDB(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sync_state(key,value,updatedAtMs) VALUES(?,?,1)`, syncStateHistoryCursorCommitted, "100"); err != nil {
		t.Fatal(err)
	}
	svc.History[""] = &gmailapi.ListHistoryResponse{History: []*gmailapi.History{{Id: 105, LabelsAdded: []*gmailapi.HistoryLabelAdded{{Message: &gmailapi.Message{Id: "m1"}, LabelIds: []string{"Label_2"}}}}}}

	if err := g.SyncHistoryWithDB(db); err != nil {
		t.Fatal(err)
	}
	var historyID uint64
	var messageID string
	var op int
	var flags int
	var labels string
	if err := db.QueryRow(`SELECT historyId, messageId, op, flags, labels FROM gmail_history_journal ORDER BY historyId, messageId, op LIMIT 1`).Scan(&historyID, &messageID, &op, &flags, &labels); err != nil {
		t.Fatal(err)
	}
	if historyID != 105 || messageID != "m1" || op != int(WRITE_LABELS) || flags != historyJournalFlagExistsAfter {
		t.Fatalf("unexpected journal row historyId=%d messageId=%s op=%d flags=%d", historyID, messageID, op, flags)
	}
	if !strings.Contains(labels, "One") || !strings.Contains(labels, "Two") {
		t.Fatalf("journal labels did not preserve final state: %q", labels)
	}
}

func TestSyncHistoryWithDBExpiredStartHistoryReplaysFromJournal(t *testing.T) {
	g, svc, dir := getTestClient()
	db := openTestDB(t, filepath.Join(dir, "history_replay.db"))
	defer db.Close()
	if err := ensureListPagesSchema(db); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sync_state(key,value,updatedAtMs) VALUES(?,?,1)`, syncStateHistoryCursorCommitted, "100"); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO gmail_history_journal(historyId,messageId,op,flags,labels,appliedAtMs) VALUES(105,'m2',?,?,?,1)`, WRITE_LABELS, historyJournalFlagExistsAfter, "One\x1fTwo"); err != nil {
		t.Fatal(err)
	}
	raw := base64.URLEncoding.EncodeToString([]byte("From: a@b\nTo: c@d\nSubject: hi\n\nbody"))
	svc.Msgs["m2"] = raw
	svc.HistoryErr[""] = &googleapi.Error{Code: 404, Message: "history expired"}
	g.SetStartHistoryIdOverride(100)

	if err := g.SyncHistoryWithDB(db); err != nil {
		t.Fatal(err)
	}
	fn, err := g.dir.GetFile(messageMaildirKey("m2"))
	if err != nil {
		t.Fatalf("expected replayed message to exist: %v", err)
	}
	b, err := os.ReadFile(fn)
	if err != nil {
		t.Fatal(err)
	}
	txt := string(b)
	if !strings.Contains(txt, "X-Keywords: One") || !strings.Contains(txt, "X-Keywords: Two") {
		t.Fatalf("expected replayed labels in message headers")
	}
}
