package api

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/muty/nexus/internal/store"
)

// TestWriteConnectorUpdateError_DispatchesByErrorKind covers every
// branch of writeConnectorUpdateError — the three error kinds it
// translates into HTTP responses (404 / 409 / 400).
func TestWriteConnectorUpdateError_DispatchesByErrorKind(t *testing.T) {
	cases := []struct {
		name     string
		err      error
		wantCode int
		wantMsg  string
	}{
		{"not found", store.ErrNotFound, http.StatusNotFound, errConnectorNotFound},
		{"duplicate name", store.ErrDuplicateName, http.StatusConflict, "connector name already exists"},
		{"generic validation", errors.New("bad config"), http.StatusBadRequest, "bad config"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			writeConnectorUpdateError(w, tc.err)
			if w.Code != tc.wantCode {
				t.Errorf("status = %d, want %d", w.Code, tc.wantCode)
			}
			var resp APIResponse
			if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
				t.Fatalf("decode: %v", err)
			}
			if resp.Error != tc.wantMsg {
				t.Errorf("error = %q, want %q", resp.Error, tc.wantMsg)
			}
		})
	}
}

func TestValidateConnectorName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		ok   bool
	}{
		{"plain", "mailbox", true},
		{"with space", "My Email", true},
		{"unicode", "Работа", true},
		{"dot-in-name", "backup.2026", true},
		{"empty", "", false},
		{"whitespace only", "   ", false},
		{"dot", ".", false},
		{"dotdot", "..", false},
		{"forward slash traversal", "../../../../var/lib/nexus", false},
		{"backslash", `a\b`, false},
		{"embedded slash", "a/b", false},
		{"null byte", "a\x00b", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := validateConnectorName(tc.in)
			if tc.ok && err != nil {
				t.Errorf("validateConnectorName(%q) = %v, want nil", tc.in, err)
			}
			if !tc.ok && err == nil {
				t.Errorf("validateConnectorName(%q) = nil, want error", tc.in)
			}
		})
	}
}
