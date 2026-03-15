package maildir

import (
	"io"
	"net/mail"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestMessageTimestampFromHeadersTopmostReceivedWins(t *testing.T) {
	h := mail.Header{}
	h["Received"] = []string{
		"from mx-new.example by local.example; Tue, 15 Mar 2022 10:20:30 +0000",
		"from mx-old.example by local.example; Mon, 14 Mar 2022 09:10:20 +0000",
	}
	ts, ok := messageTimestampFromHeaders(h)
	if !ok {
		t.Fatalf("expected timestamp from Received")
	}
	want, _ := time.Parse(time.RFC1123Z, "Tue, 15 Mar 2022 10:20:30 +0000")
	if !ts.Equal(want) {
		t.Fatalf("timestamp=%v want=%v", ts, want)
	}
}

func TestMessageTimestampFromHeadersFallsBackToDate(t *testing.T) {
	h := mail.Header{}
	h["Date"] = []string{"Wed, 16 Mar 2022 11:21:31 +0000"}
	ts, ok := messageTimestampFromHeaders(h)
	if !ok {
		t.Fatalf("expected timestamp from Date")
	}
	want, _ := time.Parse(time.RFC1123Z, "Wed, 16 Mar 2022 11:21:31 +0000")
	if !ts.Equal(want) {
		t.Fatalf("timestamp=%v want=%v", ts, want)
	}
}

func TestMessageTimestampFromHeadersMissingOrInvalidReturnsNone(t *testing.T) {
	h := mail.Header{}
	h["Received"] = []string{"from mx without date separator"}
	h["Date"] = []string{"not-a-date"}
	if _, ok := messageTimestampFromHeaders(h); ok {
		t.Fatalf("expected no timestamp")
	}
}

func TestDeliverWithKeyAppliesMtimeFromHeaders(t *testing.T) {
	dir := t.TempDir()
	md, err := Create(dir)
	if err != nil {
		t.Fatal(err)
	}
	ts := "Thu, 17 Mar 2022 12:22:32 +0000"
	msg := &mail.Message{
		Header: mail.Header{
			"From":     []string{"a@example.com"},
			"To":       []string{"b@example.com"},
			"Subject":  []string{"hello"},
			"Received": []string{"from mx by local; " + ts},
		},
		Body: io.NopCloser(strings.NewReader("body")),
	}
	key := Key("id.mail")
	if _, err := md.DeliverWithKey(msg, key); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "new", string(key)))
	if err != nil {
		t.Fatal(err)
	}
	want, _ := time.Parse(time.RFC1123Z, ts)
	if !fi.ModTime().Equal(want) {
		t.Fatalf("mtime=%v want=%v", fi.ModTime(), want)
	}
}
