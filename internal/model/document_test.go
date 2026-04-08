package model

import "testing"

func TestDocumentID_Deterministic(t *testing.T) {
	id1 := DocumentID("imap", "my-mail", "INBOX:42")
	id2 := DocumentID("imap", "my-mail", "INBOX:42")

	if id1 != id2 {
		t.Errorf("same inputs should produce same ID: %s != %s", id1, id2)
	}
}

func TestDocumentID_DifferentInputs(t *testing.T) {
	a := DocumentID("imap", "my-mail", "INBOX:42")
	b := DocumentID("imap", "my-mail", "INBOX:43")
	c := DocumentID("filesystem", "my-mail", "INBOX:42")
	d := DocumentID("imap", "other", "INBOX:42")

	if a == b {
		t.Error("different sourceID should produce different IDs")
	}
	if a == c {
		t.Error("different sourceType should produce different IDs")
	}
	if a == d {
		t.Error("different sourceName should produce different IDs")
	}
}

func TestDocumentID_NotNil(t *testing.T) {
	id := DocumentID("test", "test", "test")
	if id.String() == "00000000-0000-0000-0000-000000000000" {
		t.Error("DocumentID should not return nil UUID")
	}
}
