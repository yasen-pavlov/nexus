//go:build integration

package store

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/muty/nexus/internal/model"
)

func TestSearch(t *testing.T) {
	st := newTestStore(t)
	ctx := context.Background()

	docs := []model.Document{
		{
			ID: uuid.New(), SourceType: "filesystem", SourceName: "test", SourceID: "recipe.txt",
			Title: "Pasta Recipe", Content: "Cook spaghetti in boiling water with garlic and olive oil",
			Metadata: map[string]any{}, Visibility: "private", CreatedAt: time.Now(),
		},
		{
			ID: uuid.New(), SourceType: "filesystem", SourceName: "test", SourceID: "notes.txt",
			Title: "Meeting Notes", Content: "Discussed the quarterly budget and project timeline",
			Metadata: map[string]any{}, Visibility: "private", CreatedAt: time.Now(),
		},
		{
			ID: uuid.New(), SourceType: "filesystem", SourceName: "test", SourceID: "shopping.txt",
			Title: "Shopping List", Content: "Buy olive oil and fresh garlic from the market",
			Metadata: map[string]any{}, Visibility: "private", CreatedAt: time.Now(),
		},
	}

	for i := range docs {
		if err := st.UpsertDocument(ctx, &docs[i]); err != nil {
			t.Fatalf("upsert doc %d failed: %v", i, err)
		}
	}

	t.Run("basic search", func(t *testing.T) {
		result, err := st.Search(ctx, model.SearchRequest{Query: "garlic", Limit: 10})
		if err != nil {
			t.Fatalf("search failed: %v", err)
		}
		if result.TotalCount != 2 {
			t.Errorf("expected 2 results for 'garlic', got %d", result.TotalCount)
		}
		if result.Query != "garlic" {
			t.Errorf("expected query 'garlic', got %q", result.Query)
		}
	})

	t.Run("headline contains marks", func(t *testing.T) {
		result, err := st.Search(ctx, model.SearchRequest{Query: "budget", Limit: 10})
		if err != nil {
			t.Fatalf("search failed: %v", err)
		}
		if result.TotalCount != 1 {
			t.Fatalf("expected 1 result, got %d", result.TotalCount)
		}
		if !strings.Contains(result.Documents[0].Headline, "<mark>") {
			t.Errorf("expected headline to contain <mark>, got %q", result.Documents[0].Headline)
		}
	})

	t.Run("no results", func(t *testing.T) {
		result, err := st.Search(ctx, model.SearchRequest{Query: "xyznonexistent", Limit: 10})
		if err != nil {
			t.Fatalf("search failed: %v", err)
		}
		if result.TotalCount != 0 {
			t.Errorf("expected 0 results, got %d", result.TotalCount)
		}
	})

	t.Run("pagination", func(t *testing.T) {
		result, err := st.Search(ctx, model.SearchRequest{Query: "garlic", Limit: 1})
		if err != nil {
			t.Fatalf("search failed: %v", err)
		}
		if result.TotalCount != 2 {
			t.Errorf("expected total 2, got %d", result.TotalCount)
		}
		if len(result.Documents) != 1 {
			t.Errorf("expected 1 document with limit=1, got %d", len(result.Documents))
		}

		result2, err := st.Search(ctx, model.SearchRequest{Query: "garlic", Limit: 1, Offset: 1})
		if err != nil {
			t.Fatalf("search failed: %v", err)
		}
		if len(result2.Documents) != 1 {
			t.Fatalf("expected 1 document on page 2, got %d", len(result2.Documents))
		}
		if result.Documents[0].ID == result2.Documents[0].ID {
			t.Error("expected different document on page 2")
		}
	})

	t.Run("default limit", func(t *testing.T) {
		result, err := st.Search(ctx, model.SearchRequest{Query: "garlic"})
		if err != nil {
			t.Fatalf("search failed: %v", err)
		}
		if result.TotalCount != 2 {
			t.Errorf("expected 2 results, got %d", result.TotalCount)
		}
	})
}
