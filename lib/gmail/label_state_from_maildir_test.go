package gmail

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	gmailapi "google.golang.org/api/gmail/v1"
)

func TestHistoryLabelDeltaMissingFileTriggersSelfHeal(t *testing.T) {
	g, svc, dir := getTestClient()
	db := openTestDB(t, filepath.Join(dir, "history_missing_file.db"))
	defer db.Close()
	if err := ensureListPagesSchema(db); err != nil {
		t.Fatal(err)
	}
	seedListRows(t, db, []listedMessage{{MessageID: "m2"}})
	if _, err := db.Exec(`INSERT INTO gmail_labels(id, name, type, updatedAtMs) VALUES('Label_1','One','user',1),('Label_2','Two','user',1)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO sync_state(key,value,updatedAtMs) VALUES(?,?,1)`, syncStateHistoryCursorCommitted, "100"); err != nil {
		t.Fatal(err)
	}

	raw := base64.URLEncoding.EncodeToString([]byte("From: a@b\nTo: c@d\nSubject: hi\n\nbody"))
	svc.Msgs["m2"] = raw
	svc.Metadata["m2"] = &gmailapi.Message{Id: "m2", LabelIds: []string{"Label_1"}}
	svc.History[""] = &gmailapi.ListHistoryResponse{History: []*gmailapi.History{{
		Id: 105,
		LabelsAdded: []*gmailapi.HistoryLabelAdded{{
			Message:  &gmailapi.Message{Id: "m2"},
			LabelIds: []string{"Label_2"},
		}},
	}}}

	if err := g.SyncHistoryWithDB(db); err != nil {
		t.Fatal(err)
	}
	fn, err := g.dir.GetFile(messageMaildirKey("m2"))
	if err != nil {
		t.Fatalf("expected message to be self-healed in maildir: %v", err)
	}
	b, err := os.ReadFile(fn)
	if err != nil {
		t.Fatal(err)
	}
	txt := string(b)
	if !strings.Contains(txt, "X-Keywords: One") || !strings.Contains(txt, "X-Keywords: Two") {
		t.Fatalf("expected mapped labels One+Two in headers after self-heal")
	}
}
