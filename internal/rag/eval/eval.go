// Package eval is the offline RAG quality harness behind `make rag-eval`.
//
// It runs a set of golden (query, expectation) cases through the live RAG
// orchestrator, scores each with a deterministic citation check plus an
// LLM-as-judge for faithfulness / relevance / abstention, and renders a
// markdown report diffed against the previous baseline so ranking, model,
// or prompt changes that regress quality are visible.
//
// The package is deliberately pure: the live orchestrator and judge model
// are injected as a TurnRunner and a GenerateFunc, so the scoring, report,
// and baseline-diff logic are unit-tested without any network or index.
package eval

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// GoldenCase is one (query, expectation) tuple loaded from a golden YAML.
type GoldenCase struct {
	Name string `yaml:"name"`
	// Query is the user question fed to the orchestrator.
	Query string `yaml:"query"`
	// Lang is an informational tag (en/de/bg) for report grouping.
	Lang string `yaml:"lang,omitempty"`
	// MustCiteDocIDs, when set, are chunk handles the answer must cite
	// (subset check). Empty skips the citation check.
	MustCiteDocIDs []string `yaml:"must_cite_doc_ids,omitempty"`
	// ShouldAbstain marks a question whose answer is NOT in the index:
	// the assistant should say it can't find it rather than hallucinate.
	ShouldAbstain bool `yaml:"should_abstain,omitempty"`
	// Notes is free text for the human maintaining the set.
	Notes string `yaml:"notes,omitempty"`
}

// TurnOutput is what running one query through the RAG pipeline produced.
type TurnOutput struct {
	Answer      string
	CitedDocIDs []string
	// Evidence is short text (titles + headlines) of the retrieved chunks,
	// passed to the faithfulness judge as the grounding context.
	Evidence []string
}

// TurnRunner runs one query end-to-end. cmd/rag-eval implements it via the
// orchestrator; tests pass a stub.
type TurnRunner func(ctx context.Context, query string) (TurnOutput, error)

// GenerateFunc is a one-shot LLM call (system + user → text). cmd/rag-eval
// implements it via the judge model's generator; tests pass a stub.
type GenerateFunc func(ctx context.Context, system, user string) (string, error)

// CaseResult is the scored outcome for one golden case.
type CaseResult struct {
	Name  string
	Lang  string
	Query string
	Error string // non-empty when the turn or judge failed

	// CitedCorrect is nil when the case sets no must_cite_doc_ids.
	CitedCorrect *bool
	MissingCites []string

	Faithful  *bool   // nil = not judged (abstain cases / error)
	Relevance *string // "yes" | "no" | "partial" | nil
	Abstained *bool   // only set for ShouldAbstain cases

	Answer string
}

// Passed reports whether a case met every expectation that applied to it.
func (r CaseResult) Passed() bool {
	if r.Error != "" {
		return false
	}
	// Abstain cases: the only success criterion is that the assistant
	// declined. Relevance ("does it address the question") is expected to
	// be no/partial here and must not count against it.
	if r.Abstained != nil {
		return *r.Abstained
	}
	if r.CitedCorrect != nil && !*r.CitedCorrect {
		return false
	}
	if r.Faithful != nil && !*r.Faithful {
		return false
	}
	if r.Relevance != nil && *r.Relevance == "no" {
		return false
	}
	return true
}

// Report is the full run.
type Report struct {
	Model      string
	JudgeModel string
	Results    []CaseResult
}

// PassCount returns how many cases passed.
func (rep Report) PassCount() int {
	n := 0
	for _, r := range rep.Results {
		if r.Passed() {
			n++
		}
	}
	return n
}

// LoadGolden reads every *.yaml in dir into GoldenCases, sorted by name for
// stable report ordering.
func LoadGolden(dir string) ([]GoldenCase, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil, fmt.Errorf("read golden dir: %w", err)
	}
	var cases []GoldenCase
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".yaml") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", e.Name(), err)
		}
		// Each file may hold one case or a list.
		var list []GoldenCase
		if err := yaml.Unmarshal(raw, &list); err == nil && len(list) > 0 {
			cases = append(cases, list...)
			continue
		}
		var one GoldenCase
		if err := yaml.Unmarshal(raw, &one); err != nil {
			return nil, fmt.Errorf("parse %s: %w", e.Name(), err)
		}
		if one.Query != "" {
			cases = append(cases, one)
		}
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].Name < cases[j].Name })
	return cases, nil
}

// citedCorrect reports whether every must-cite id appears in got, and which
// are missing.
func citedCorrect(must, got []string) (bool, []string) {
	seen := make(map[string]struct{}, len(got))
	for _, g := range got {
		seen[g] = struct{}{}
	}
	var missing []string
	for _, m := range must {
		if _, ok := seen[m]; !ok {
			missing = append(missing, m)
		}
	}
	return len(missing) == 0, missing
}

func boolPtr(b bool) *bool { return &b }
