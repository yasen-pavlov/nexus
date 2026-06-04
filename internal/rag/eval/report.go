package eval

import (
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"strings"
)

// Baseline maps a case name to whether it passed on the previous run. Used
// to flag regressions (passed → failed) in the report.
type Baseline map[string]bool

// LoadBaseline reads a baseline JSON. A missing file is not an error — the
// first run has no baseline to diff against.
func LoadBaseline(path string) (Baseline, error) {
	raw, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return Baseline{}, nil
	}
	if err != nil {
		return nil, err
	}
	var b Baseline
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, fmt.Errorf("parse baseline: %w", err)
	}
	return b, nil
}

// SaveBaseline writes the current run's per-case pass/fail as the new
// baseline.
func SaveBaseline(path string, rep Report) error {
	b := make(Baseline, len(rep.Results))
	for _, r := range rep.Results {
		b[r.Name] = r.Passed()
	}
	raw, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o600)
}

func tri(b *bool) string {
	if b == nil {
		return "–"
	}
	if *b {
		return "✓"
	}
	return "✗"
}

func relCell(s *string) string {
	if s == nil {
		return "–"
	}
	return *s
}

// delta compares the current pass state against the baseline.
func delta(prev Baseline, name string, passed bool) string {
	was, known := prev[name]
	switch {
	case !known:
		return "new"
	case was && !passed:
		return "⚠ REGRESSED"
	case !was && passed:
		return "✓ fixed"
	default:
		return ""
	}
}

// RenderMarkdown renders the run as a markdown report, flagging regressions
// against prev.
func RenderMarkdown(rep Report, prev Baseline) string {
	var b strings.Builder

	fmt.Fprintf(&b, "# RAG eval report\n\n")
	fmt.Fprintf(&b, "- Model: `%s`\n", rep.Model)
	fmt.Fprintf(&b, "- Judge: `%s`\n", rep.JudgeModel)
	fmt.Fprintf(&b, "- **Passed: %d / %d**\n\n", rep.PassCount(), len(rep.Results))

	renderRegressionCallout(&b, rep, prev)
	renderResultsTable(&b, rep, prev)
	renderFailures(&b, rep)

	return b.String()
}

// renderRegressionCallout writes the regression banner first — that's the
// reason this report exists.
func renderRegressionCallout(b *strings.Builder, rep Report, prev Baseline) {
	var regressions []string
	for _, r := range rep.Results {
		if delta(prev, r.Name, r.Passed()) == "⚠ REGRESSED" {
			regressions = append(regressions, r.Name)
		}
	}
	if len(regressions) > 0 {
		fmt.Fprintf(b, "> ⚠ **%d regression(s):** %s\n\n", len(regressions), strings.Join(regressions, ", "))
	}
}

// renderResultsTable writes the per-case scoring table.
func renderResultsTable(b *strings.Builder, rep Report, prev Baseline) {
	fmt.Fprintf(b, "| Case | Lang | Cited | Faithful | Relevant | Abstain | Pass | Δ |\n")
	fmt.Fprintf(b, "|------|------|-------|----------|----------|---------|------|---|\n")
	for _, r := range rep.Results {
		pass := "✓"
		if !r.Passed() {
			pass = "✗"
		}
		fmt.Fprintf(b, "| %s | %s | %s | %s | %s | %s | %s | %s |\n",
			r.Name, dash(r.Lang), tri(r.CitedCorrect), tri(r.Faithful),
			relCell(r.Relevance), tri(r.Abstained), pass, delta(prev, r.Name, r.Passed()))
	}
}

// renderFailures writes the per-case detail for failures so the report is
// actionable.
func renderFailures(b *strings.Builder, rep Report) {
	var failed []CaseResult
	for _, r := range rep.Results {
		if !r.Passed() {
			failed = append(failed, r)
		}
	}
	if len(failed) == 0 {
		return
	}
	fmt.Fprintf(b, "\n## Failures\n\n")
	for _, r := range failed {
		fmt.Fprintf(b, "### %s\n", r.Name)
		fmt.Fprintf(b, "- query: %s\n", r.Query)
		if r.Error != "" {
			fmt.Fprintf(b, "- error: %s\n", r.Error)
		}
		if len(r.MissingCites) > 0 {
			fmt.Fprintf(b, "- missing citations: %s\n", strings.Join(r.MissingCites, ", "))
		}
		if r.Answer != "" {
			fmt.Fprintf(b, "- answer: %s\n", truncate(r.Answer, 280))
		}
		b.WriteString("\n")
	}
}

func dash(s string) string {
	if s == "" {
		return "–"
	}
	return s
}

func truncate(s string, n int) string {
	s = strings.ReplaceAll(s, "\n", " ")
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
