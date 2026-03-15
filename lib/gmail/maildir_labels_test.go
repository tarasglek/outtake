package gmail

import (
	"net/mail"
	"strings"
	"testing"
)

func readMsg(t *testing.T, raw string) *mail.Message {
	t.Helper()
	m, err := mail.ReadMessage(strings.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestMaildirLabelsNoHeader(t *testing.T) {
	m := readMsg(t, "From: a@b\nTo: c@d\nSubject: hi\n\nbody")
	labels, err := readLabelsFromMaildirMessage(m)
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) != 0 {
		t.Fatalf("labels=%v expected empty", labels)
	}
}

func TestMaildirLabelsMultipleHeaders(t *testing.T) {
	m := readMsg(t, "From: a@b\nX-Keywords: One\nX-Keywords: Two\n\nbody")
	labels, err := readLabelsFromMaildirMessage(m)
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) != 2 || labels[0] != "One" || labels[1] != "Two" {
		t.Fatalf("labels=%v expected [One Two]", labels)
	}
}

func TestMaildirLabelsDeduplicatesAndTrims(t *testing.T) {
	m := readMsg(t, "From: a@b\nX-Keywords:  One , Two,One ,   ,Two\n\nbody")
	labels, err := readLabelsFromMaildirMessage(m)
	if err != nil {
		t.Fatal(err)
	}
	if len(labels) != 2 || labels[0] != "One" || labels[1] != "Two" {
		t.Fatalf("labels=%v expected [One Two]", labels)
	}
}
