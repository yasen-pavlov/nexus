package cliclient

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/muty/nexus/internal/model"
)

func TestChatsClient(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/chats", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			writeData(w, 201, model.Chat{Title: "t"})
		case http.MethodGet:
			writeData(w, 200, map[string]any{
				"chats": []map[string]any{{"id": "00000000-0000-0000-0000-000000000001", "title": "Hello"}},
				"total": 1,
			})
		}
	})
	mux.HandleFunc("/api/chats/", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet:
			writeData(w, 200, map[string]any{
				"chat":     model.Chat{Title: "c"},
				"messages": []model.ChatMessage{{Content: "hi"}},
			})
		case http.MethodDelete:
			w.WriteHeader(204)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := New(srv.URL, "tok")
	ctx := context.Background()

	if _, err := c.CreateChat(ctx); err != nil {
		t.Fatalf("create: %v", err)
	}
	chats, total, err := c.ListChats(ctx, 10, 5)
	if err != nil || total != 1 || len(chats) != 1 || chats[0].Title != "Hello" {
		t.Fatalf("list: %v total=%d %+v", err, total, chats)
	}
	detail, err := c.GetChat(ctx, "00000000-0000-0000-0000-000000000001")
	if err != nil || len(detail.Messages) != 1 {
		t.Fatalf("get: %v %+v", err, detail)
	}
	if err := c.DeleteChat(ctx, "x"); err != nil {
		t.Fatalf("delete: %v", err)
	}
}

func TestConnectorsClient(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/api/connectors", func(w http.ResponseWriter, _ *http.Request) {
		writeData(w, 200, []map[string]any{
			{"id": "00000000-0000-0000-0000-000000000002", "type": "imap", "name": "Mail", "status": "active"},
		})
	})
	mux.HandleFunc("/api/sync", func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodPost:
			writeData(w, 202, []SyncJob{{ID: "j1", ConnectorName: "Mail", Status: "running"}})
		case http.MethodGet:
			writeData(w, 200, []SyncJob{{ID: "j1", ConnectorName: "Mail", Status: "running"}})
		}
	})
	mux.HandleFunc("/api/sync/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/sync/busy" {
			w.WriteHeader(409)
			_, _ = w.Write([]byte(`{"error":"sync already running for Mail"}`))
			return
		}
		writeData(w, 202, SyncJob{ID: "j2", ConnectorName: "Mail", Status: "running"})
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	c := New(srv.URL, "tok")
	ctx := context.Background()

	conns, err := c.ListConnectors(ctx)
	if err != nil || len(conns) != 1 || conns[0].Status != "active" || conns[0].Type != "imap" {
		t.Fatalf("list connectors: %v %+v", err, conns)
	}
	job, err := c.TriggerSync(ctx, "ok")
	if err != nil || job.ID != "j2" {
		t.Fatalf("trigger: %v %+v", err, job)
	}
	_, err = c.TriggerSync(ctx, "busy")
	asAPIError(t, err, 409, "sync already running for Mail")

	if jobs, err := c.SyncAll(ctx); err != nil || len(jobs) != 1 {
		t.Fatalf("syncall: %v %+v", err, jobs)
	}
	if jobs, err := c.ListSyncJobs(ctx); err != nil || len(jobs) != 1 {
		t.Fatalf("listjobs: %v %+v", err, jobs)
	}
}

func TestPR2ClientErrorPaths(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(500)
		_, _ = w.Write([]byte(`{"error":"boom"}`))
	}))
	defer srv.Close()
	c := New(srv.URL, "t")
	ctx := context.Background()

	if _, err := c.CreateChat(ctx); err == nil {
		t.Fatal("CreateChat should error on 500")
	}
	if _, _, err := c.ListChats(ctx, 0, 0); err == nil {
		t.Fatal("ListChats should error on 500")
	}
	if _, err := c.GetChat(ctx, "x"); err == nil {
		t.Fatal("GetChat should error on 500")
	}
	if err := c.DeleteChat(ctx, "x"); err == nil {
		t.Fatal("DeleteChat should error on 500")
	}
	if _, err := c.ListConnectors(ctx); err == nil {
		t.Fatal("ListConnectors should error on 500")
	}
	if _, err := c.TriggerSync(ctx, "x"); err == nil {
		t.Fatal("TriggerSync should error on 500")
	}
	if _, err := c.SyncAll(ctx); err == nil {
		t.Fatal("SyncAll should error on 500")
	}
	if _, err := c.ListSyncJobs(ctx); err == nil {
		t.Fatal("ListSyncJobs should error on 500")
	}
}
