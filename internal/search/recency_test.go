package search

import (
	"testing"
	"time"

	"github.com/muty/nexus/internal/model"
)

func TestApplyRecencyDecay_RecentScoresHigher(t *testing.T) {
	result := &model.SearchResult{
		Documents: []model.DocumentHit{
			{Document: model.Document{Title: "Old", SourceType: "imap", CreatedAt: time.Now().AddDate(0, -6, 0)}, Rank: 0.9},
			{Document: model.Document{Title: "New", SourceType: "imap", CreatedAt: time.Now().Add(-time.Hour)}, Rank: 0.9},
		},
	}

	ApplyRecencyDecay(result, DefaultRankingConfig())

	// Same relevance, but new should rank higher after decay
	if result.Documents[0].Title != "New" {
		t.Errorf("expected New first, got %q", result.Documents[0].Title)
	}
	if result.Documents[0].Rank <= result.Documents[1].Rank {
		t.Errorf("new doc score (%f) should be > old doc score (%f)", result.Documents[0].Rank, result.Documents[1].Rank)
	}
}

func TestApplyRecencyDecay_OldDocKeepsFloor(t *testing.T) {
	cfg := DefaultRankingConfig()
	result := &model.SearchResult{
		Documents: []model.DocumentHit{
			{Document: model.Document{Title: "Ancient", SourceType: "imap", CreatedAt: time.Now().AddDate(-5, 0, 0)}, Rank: 1.0},
		},
	}

	ApplyRecencyDecay(result, cfg)

	// Very old imap doc should keep at least its source-specific floor of its score
	imapFloor := cfg.SourceRecencyFloor["imap"]
	if result.Documents[0].Rank < imapFloor*0.99 {
		t.Errorf("ancient doc score = %f, should be >= %f (imap floor)", result.Documents[0].Rank, imapFloor)
	}
}

func TestApplyRecencyDecay_BrandNewFullScore(t *testing.T) {
	result := &model.SearchResult{
		Documents: []model.DocumentHit{
			{Document: model.Document{Title: "Just now", SourceType: "imap", CreatedAt: time.Now()}, Rank: 0.8},
		},
	}

	ApplyRecencyDecay(result, DefaultRankingConfig())

	// Brand new doc should keep nearly its full score (factor ≈ 1.0)
	if result.Documents[0].Rank < 0.79 {
		t.Errorf("new doc score = %f, should be ~0.8", result.Documents[0].Rank)
	}
}

func TestApplyRecencyDecay_SourceSpecificHalfLives(t *testing.T) {
	age := 30 * 24 * time.Hour // 30 days old
	created := time.Now().Add(-age)

	telegramResult := &model.SearchResult{
		Documents: []model.DocumentHit{
			{Document: model.Document{SourceType: "telegram", CreatedAt: created}, Rank: 1.0},
		},
	}
	paperlessResult := &model.SearchResult{
		Documents: []model.DocumentHit{
			{Document: model.Document{SourceType: "paperless", CreatedAt: created}, Rank: 1.0},
		},
	}

	cfg := DefaultRankingConfig()
	ApplyRecencyDecay(telegramResult, cfg)
	ApplyRecencyDecay(paperlessResult, cfg)

	// Telegram (14-day half-life) should decay more than Paperless (180-day half-life)
	if telegramResult.Documents[0].Rank >= paperlessResult.Documents[0].Rank {
		t.Errorf("telegram (%f) should decay more than paperless (%f) at 30 days",
			telegramResult.Documents[0].Rank, paperlessResult.Documents[0].Rank)
	}
}

func TestApplyRecencyDecay_ZeroCreatedAtUnchanged(t *testing.T) {
	result := &model.SearchResult{
		Documents: []model.DocumentHit{
			{Document: model.Document{Title: "No date"}, Rank: 0.5},
		},
	}

	ApplyRecencyDecay(result, DefaultRankingConfig())

	if result.Documents[0].Rank != 0.5 {
		t.Errorf("score changed for zero CreatedAt: %f, want 0.5", result.Documents[0].Rank)
	}
}

func TestApplyRecencyDecay_EmptyResults(t *testing.T) {
	result := &model.SearchResult{}
	ApplyRecencyDecay(result, DefaultRankingConfig()) // should not panic
}

func TestApplyRecencyDecay_UnknownSourceUsesDefault(t *testing.T) {
	result := &model.SearchResult{
		Documents: []model.DocumentHit{
			{Document: model.Document{SourceType: "unknown_source", CreatedAt: time.Now().AddDate(0, -2, 0)}, Rank: 1.0},
		},
	}

	ApplyRecencyDecay(result, DefaultRankingConfig())

	// Should use defaultHalfLife (60 days) and defaultRecencyFloor,
	// score should be reduced but not below the floor.
	if result.Documents[0].Rank >= 1.0 || result.Documents[0].Rank < defaultRecencyFloor {
		t.Errorf("score = %f, expected between %f and 1.0", result.Documents[0].Rank, defaultRecencyFloor)
	}
}

func TestApplyRecencyDecay_RelevanceDominates(t *testing.T) {
	result := &model.SearchResult{
		Documents: []model.DocumentHit{
			{Document: model.Document{Title: "Highly relevant old", SourceType: "imap", CreatedAt: time.Now().AddDate(-1, 0, 0)}, Rank: 0.95},
			{Document: model.Document{Title: "Weakly relevant new", SourceType: "imap", CreatedAt: time.Now()}, Rank: 0.3},
		},
	}

	ApplyRecencyDecay(result, DefaultRankingConfig())

	// Highly relevant old doc should still beat weakly relevant new doc
	if result.Documents[0].Title != "Highly relevant old" {
		t.Errorf("expected highly relevant old doc first, got %q (scores: %f, %f)",
			result.Documents[0].Title, result.Documents[0].Rank, result.Documents[1].Rank)
	}
}

func TestApplyRecencyDecay_ConfigOverrides(t *testing.T) {
	// Override the imap half-life to 1 day — a doc that's 30 days old
	// should now decay aggressively despite the default being 30 days.
	cfg := DefaultRankingConfig()
	cfg.SourceHalfLifeDays["imap"] = 1
	cfg.SourceRecencyFloor["imap"] = 0.1

	result := &model.SearchResult{
		Documents: []model.DocumentHit{
			{Document: model.Document{SourceType: "imap", CreatedAt: time.Now().AddDate(0, -1, 0)}, Rank: 1.0},
		},
	}
	ApplyRecencyDecay(result, cfg)

	if result.Documents[0].Rank >= 0.5 {
		t.Errorf("override failed: 30-day-old imap doc with 1-day half-life scored %f, expected < 0.5", result.Documents[0].Rank)
	}
}
