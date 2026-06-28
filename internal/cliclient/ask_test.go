package cliclient

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func runParse(t *testing.T, stream string) (events, datas []string) {
	t.Helper()
	err := parseSSE(strings.NewReader(stream), func(ev string, d []byte) error {
		events = append(events, ev)
		datas = append(datas, string(d))
		return nil
	})
	if err != nil {
		t.Fatalf("parseSSE: %v", err)
	}
	return events, datas
}

func TestParseSSEBasic(t *testing.T) {
	stream := "event: text\ndata: {\"delta\":\"Hello \"}\n\n" +
		": keep-alive comment\n\n" +
		"event: text\ndata: {\"delta\":\"world\"}\n\n" +
		"event: done\ndata: {\"stop_reason\":\"end_turn\"}\n\n"
	events, datas := runParse(t, stream)
	if strings.Join(events, ",") != "text,text,done" {
		t.Fatalf("events = %v (comment frame should be dropped)", events)
	}
	if datas[0] != `{"delta":"Hello "}` {
		t.Fatalf("data[0] = %q", datas[0])
	}
}

func TestParseSSEMultilineAndCRLF(t *testing.T) {
	events, datas := runParse(t, "event: x\r\ndata: a\r\ndata: b\r\n\r\n")
	if len(events) != 1 || events[0] != "x" || datas[0] != "a\nb" {
		t.Fatalf("events=%v datas=%v", events, datas)
	}
}

func TestParseSSEEOFFlush(t *testing.T) {
	// No trailing blank line; EOF must still dispatch the final frame.
	events, _ := runParse(t, "event: done\ndata: {}")
	if len(events) != 1 || events[0] != "done" {
		t.Fatalf("events = %v", events)
	}
}

func TestParseSSELargeFrameNotTruncated(t *testing.T) {
	big := strings.Repeat("x", 100_000) // far past bufio.Scanner's 64 KB cap
	_, datas := runParse(t, "event: evidence\ndata: "+big+"\n\n")
	if len(datas) != 1 || len(datas[0]) != len(big) {
		t.Fatalf("large frame truncated: got %d bytes, want %d", len(datas[0]), len(big))
	}
}

func TestParseSSEHandlerError(t *testing.T) {
	stop := errors.New("stop")
	err := parseSSE(strings.NewReader("event: a\ndata: 1\n\nevent: b\ndata: 2\n\n"), func(ev string, _ []byte) error {
		if ev == "a" {
			return stop
		}
		return nil
	})
	if !errors.Is(err, stop) {
		t.Fatalf("want stop error, got %v", err)
	}
}

func TestStreamMessageSuccess(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = io.WriteString(w, "event: text\ndata: {\"delta\":\"hi\"}\n\nevent: done\ndata: {\"stop_reason\":\"end_turn\"}\n\n")
	}))
	defer srv.Close()

	var got []string
	err := New(srv.URL, "tok").StreamMessage(context.Background(), "c1", "q", "", func(ev string, _ []byte) error {
		got = append(got, ev)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(got, ",") != "text,done" {
		t.Fatalf("got %v", got)
	}
}

func TestStreamMessage200NonStream(t *testing.T) {
	// A 200 that isn't text/event-stream is unexpected, not a frame stream.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":{"id":"m1"}}`))
	}))
	defer srv.Close()
	err := New(srv.URL, "tok").StreamMessage(context.Background(), "c1", "q", "", func(string, []byte) error { return nil })
	if err == nil || !strings.Contains(err.Error(), "unexpected non-stream response") {
		t.Fatalf("want unexpected-non-stream error, got %v", err)
	}
}

func TestStreamMessagePreStreamError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(400)
		_, _ = w.Write([]byte(`{"error":"content is required"}`))
	}))
	defer srv.Close()
	err := New(srv.URL, "tok").StreamMessage(context.Background(), "c1", "", "", func(string, []byte) error { return nil })
	asAPIError(t, err, 400, "content is required")
}
