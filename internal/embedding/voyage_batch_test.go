package embedding

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
)

func TestVoyageTokenBoundPreservesAllInputs(t *testing.T) {
	for _, size := range []int{59936, 59937, 120001} {
		t.Run(fmt.Sprint(size), func(t *testing.T) {
			var calls, seen int
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var req voyageEmbedRequest
				if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
					t.Error(err)
					return
				}
				calls++
				bound := 0
				data := make([]map[string]any, len(req.Input))
				for i, text := range req.Input {
					bound += len(text) + 64
					if text != fmt.Sprintf("%04d", seen)+strings.Repeat("界", (size-4)/3)+strings.Repeat("x", (size-4)%3) {
						t.Errorf("lost/reordered input %d", seen)
					}
					data[len(req.Input)-1-i] = map[string]any{"index": i, "embedding": []float32{float32(seen)}}
					seen++
				}
				if bound > 120000 && len(req.Input) > 1 {
					t.Errorf("unsafe bound %d", bound)
				}
				_ = json.NewEncoder(w).Encode(map[string]any{"data": data})
			}))
			defer srv.Close()
			v := NewVoyage("key", "voyage-4-large", zap.NewNop())
			v.baseURL = srv.URL
			texts := make([]string, 5)
			for i := range texts {
				texts[i] = fmt.Sprintf("%04d", i) + strings.Repeat("界", (size-4)/3) + strings.Repeat("x", (size-4)%3)
			}
			got, err := v.Embed(context.Background(), texts, InputTypeDocument)
			if err != nil {
				t.Fatal(err)
			}
			wantCalls := 5
			if size == 59936 {
				wantCalls = 3
			}
			if len(got) != len(texts) || seen != len(texts) || calls != wantCalls {
				t.Fatalf("calls=%d want=%d seen=%d vectors=%d", calls, wantCalls, seen, len(got))
			}
			for i := range got {
				if got[i][0] != float32(i) {
					t.Fatalf("vector %d misplaced", i)
				}
			}
		})
	}
}

func TestVoyageUncertainTokenBoundarySplitsWithoutLoss(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req voyageEmbedRequest
		_ = json.NewDecoder(r.Body).Decode(&req)
		calls++
		if len(req.Input) > 1 {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error_code":"TOO_MANY_TOKENS_IN_BATCH","detail":"uncertain token boundary"}`))
			return
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"data": []any{map[string]any{"index": 0, "embedding": []float32{float32(req.Input[0][0])}}}})
	}))
	defer srv.Close()
	v := NewVoyage("key", "voyage-4-large", zap.NewNop())
	v.baseURL = srv.URL
	got, err := v.Embed(context.Background(), []string{"a", "b", "c", "d", "e"}, InputTypeDocument)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 5 || calls != 9 {
		t.Fatalf("vectors=%d calls=%d", len(got), calls)
	}
	for i := range got {
		if got[i][0] != float32('a'+i) {
			t.Fatalf("vector %d misplaced", i)
		}
	}
}

func TestVoyageUnsplittableOrUnrelatedErrorFails(t *testing.T) {
	for _, code := range []string{"TOO_MANY_TOKENS_IN_BATCH", "INVALID_MODEL"} {
		t.Run(code, func(t *testing.T) {
			calls := 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				w.WriteHeader(400)
				_, _ = fmt.Fprintf(w, `{"error_code":%q}`, code)
			}))
			defer srv.Close()
			v := NewVoyage("key", "test", zap.NewNop())
			v.baseURL = srv.URL
			texts := []string{"only"}
			if code == "INVALID_MODEL" {
				texts = append(texts, "other")
			}
			if _, err := v.Embed(context.Background(), texts, InputTypeDocument); err == nil {
				t.Fatal("expected explicit failure")
			}
			if calls != 1 {
				t.Fatalf("unexpected retries: %d", calls)
			}
		})
	}
}
