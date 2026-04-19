package api

import (
	"testing"

	"github.com/muty/nexus/internal/search"
)

func TestParseIntOr(t *testing.T) {
	if got := parseIntOr("", 42); got != 42 {
		t.Errorf("parseIntOr(\"\", 42) = %d, want 42", got)
	}
	if got := parseIntOr("not-an-int", 7); got != 7 {
		t.Errorf("parseIntOr(bad, 7) = %d, want 7 (fallback)", got)
	}
	if got := parseIntOr("12", 7); got != 12 {
		t.Errorf("parseIntOr(12, 7) = %d, want 12", got)
	}
}

func TestOverlayFloatMap_InvalidJSONIsIgnored(t *testing.T) {
	// overlayFloatMap is the shared helper used by RankingManager. It's
	// defensive against a corrupt DB row — invalid JSON must leave the
	// destination map untouched rather than zero out the defaults.
	dst := map[string]float64{"existing": 1}
	overlayFloatMap(dst, `{nope`)
	if dst["existing"] != 1 {
		t.Errorf("existing entry lost after invalid overlay: %v", dst)
	}
	if _, ok := dst["nope"]; ok {
		t.Error("invalid overlay should not add entries")
	}
}

func TestOverlayFloatMap_OverlayOverrides(t *testing.T) {
	dst := map[string]float64{"imap": 30, "filesystem": 90}
	overlayFloatMap(dst, `{"imap": 7}`)
	if dst["imap"] != 7 {
		t.Errorf("overlay failed to override imap: got %f", dst["imap"])
	}
	if dst["filesystem"] != 90 {
		t.Errorf("unrelated keys must survive overlay: filesystem = %f", dst["filesystem"])
	}
}

// The ranking config defaults must include every source_type the admin UI
// can configure, otherwise a newly-persisted knob for that source falls
// back to the module-level defaultHalfLife / defaultRecencyFloor which
// don't match the curated intent.
func TestDefaultRankingConfig_IncludesAllKnownSources(t *testing.T) {
	d := search.DefaultRankingConfig()
	for _, s := range []string{"imap", "telegram", "paperless", "filesystem"} {
		if _, ok := d.SourceHalfLifeDays[s]; !ok {
			t.Errorf("defaults missing half-life for %q", s)
		}
		if _, ok := d.SourceRecencyFloor[s]; !ok {
			t.Errorf("defaults missing recency floor for %q", s)
		}
		if _, ok := d.SourceTrustWeight[s]; !ok {
			t.Errorf("defaults missing trust weight for %q", s)
		}
	}
	if d.RerankerMinScore <= 0 || d.RerankerMinScore >= 1 {
		t.Errorf("reranker floor = %f, expected 0 < x < 1", d.RerankerMinScore)
	}
}
