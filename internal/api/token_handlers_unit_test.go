package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
)

// The auth middleware guarantees claims on every protected route, but the
// handlers defend in depth with a nil-claims 401. Exercise that branch
// directly — a request with no claims on its context.
func TestTokenHandlers_NoClaims401(t *testing.T) {
	h := &handler{log: zap.NewNop()}

	cases := []struct {
		name    string
		method  string
		handler http.HandlerFunc
		body    string
	}{
		{"create", http.MethodPost, h.CreateToken, `{"name":"x"}`},
		{"list", http.MethodGet, h.ListTokens, ""},
		{"delete", http.MethodDelete, h.DeleteToken, ""},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var bodyReader *strings.Reader
			if tc.body != "" {
				bodyReader = strings.NewReader(tc.body)
			} else {
				bodyReader = strings.NewReader("")
			}
			req := httptest.NewRequest(tc.method, "/api/tokens", bodyReader)
			w := httptest.NewRecorder()
			tc.handler(w, req)
			if w.Code != http.StatusUnauthorized {
				t.Errorf("%s without claims: expected 401, got %d", tc.name, w.Code)
			}
		})
	}
}
