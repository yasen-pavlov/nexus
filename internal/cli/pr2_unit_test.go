package cli

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/muty/nexus/internal/cliclient"
)

func TestResultError(t *testing.T) {
	if (&askRenderer{errMsg: "bad"}).resultError(nil) == nil {
		t.Fatal("an error frame must surface as an error")
	}
	if (&askRenderer{}).resultError(context.Canceled) != nil {
		t.Fatal("Ctrl-C (context canceled) should be a graceful stop")
	}
	if (&askRenderer{}).resultError(errors.New("net")) == nil {
		t.Fatal("a transport error should propagate")
	}
	if (&askRenderer{stopReason: "error"}).resultError(nil) == nil {
		t.Fatal("stop_reason=error should fail")
	}
	if (&askRenderer{stopReason: "end_turn"}).resultError(nil) != nil {
		t.Fatal("a clean finish should not error")
	}
}

func TestCollectChunksDedupAndBadJSON(t *testing.T) {
	r := newAskRenderer(io.Discard, io.Discard, askOptions{})
	r.collectChunks([]byte("not json")) // ignored, no panic
	r.collectChunks([]byte(`{"chunks":[{"id":"a"},{"id":"a"},{"id":""},{"id":"b"}]}`))
	if len(r.evidence) != 2 {
		t.Fatalf("expected 2 unique non-empty chunks, got %d", len(r.evidence))
	}
}

func TestSourcesToShowModes(t *testing.T) {
	r := newAskRenderer(io.Discard, io.Discard, askOptions{})
	r.collectChunks([]byte(`{"chunks":[{"id":"a"},{"id":"b"}]}`))
	r.cited["a"] = true
	if got := r.sourcesToShow(); len(got) != 1 || got[0].DocID != "a" {
		t.Fatalf("default should show only cited: %+v", got)
	}
	r.opts.showSources = true
	if got := r.sourcesToShow(); len(got) != 2 {
		t.Fatalf("--sources should show the full union: %+v", got)
	}
}

func TestFormattersEmpty(t *testing.T) {
	var b bytes.Buffer
	formatChats(&b, nil, 0)
	formatConnectors(&b, nil)
	formatSyncJobs(&b, nil)
	formatChatDetail(&b, &cliclient.ChatDetail{})
	s := b.String()
	for _, want := range []string{"No chats.", "No connectors.", "No active or recent sync jobs.", "(no messages)"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in:\n%s", want, s)
		}
	}
	if formatTimePtr(nil) != "never" {
		t.Fatal("nil time should render 'never'")
	}
}

func TestPR2JSONOutputs(t *testing.T) {
	isolateConfig(t)
	f := newFakePR2(t)
	seedAuth(t, f.URL)

	cases := []struct {
		args []string
		want string
	}{
		{[]string{"chats", "list", "--json"}, `"title": "Recent chat"`},
		{[]string{"chats", "get", "00000000-0000-0000-0000-0000000000aa", "--json"}, `"messages"`},
		{[]string{"connectors", "list", "--json"}, `"status": "active"`},
		{[]string{"connectors", "status", "--json"}, `"id": "j1"`},
	}
	for _, c := range cases {
		out, err := run(t, "", c.args...)
		if err != nil || !strings.Contains(out, c.want) {
			t.Fatalf("%v: err=%v missing %q in:\n%s", c.args, err, c.want, out)
		}
	}
}

func TestConnectorsSyncAllEmpty(t *testing.T) {
	isolateConfig(t)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync" && r.Method == http.MethodPost {
			writeOK(w, 202, []cliclient.SyncJob{})
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()
	seedAuth(t, srv.URL)

	out, err := run(t, "", "connectors", "sync", "--all")
	if err != nil || !strings.Contains(out, "No connectors started") {
		t.Fatalf("empty sync --all: %v\n%s", err, out)
	}
}

func TestConnectorsSyncArgValidation(t *testing.T) {
	isolateConfig(t)
	f := newFakePR2(t)
	seedAuth(t, f.URL)

	if _, err := run(t, "", "connectors", "sync"); err == nil {
		t.Fatal("expected error with no id and no --all")
	}
	if _, err := run(t, "", "connectors", "sync", "id", "--all"); err == nil {
		t.Fatal("expected error passing both id and --all")
	}
}
