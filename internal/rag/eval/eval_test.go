package eval

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestParseVerdict(t *testing.T) {
	cases := map[string]string{
		"yes":              "yes",
		"Yes.":             "yes",
		"  NO":             "no",
		"partial":          "partial",
		"Partial — ...":    "partial",
		"**yes**":          "yes",
		"The answer is no": "no",
		"":                 "",
		"maybe":            "",
	}
	for in, want := range cases {
		if got := parseVerdict(in); got != want {
			t.Errorf("parseVerdict(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCitedCorrect(t *testing.T) {
	ok, missing := citedCorrect([]string{"a", "b"}, []string{"a", "b", "c"})
	if !ok || len(missing) != 0 {
		t.Errorf("subset should pass: ok=%v missing=%v", ok, missing)
	}
	ok, missing = citedCorrect([]string{"a", "x"}, []string{"a", "b"})
	if ok || len(missing) != 1 || missing[0] != "x" {
		t.Errorf("missing x: ok=%v missing=%v", ok, missing)
	}
}

func TestLoadGolden_ListAndSingle(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.yaml"), []byte(
		"- name: c1\n  query: q1\n  lang: en\n- name: c2\n  query: q2\n"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "b.yaml"), []byte(
		"name: c0\nquery: q0\nshould_abstain: true\n"), 0o600)
	_ = os.WriteFile(filepath.Join(dir, "ignore.txt"), []byte("nope"), 0o600)

	cases, err := LoadGolden(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(cases) != 3 {
		t.Fatalf("got %d cases, want 3", len(cases))
	}
	// Sorted by name: c0, c1, c2.
	if cases[0].Name != "c0" || !cases[0].ShouldAbstain {
		t.Errorf("first case = %+v", cases[0])
	}
}

// scriptedJudge returns verdicts by matching a substring of the prompt.
func scriptedJudge(byKeyword map[string]string) GenerateFunc {
	return func(_ context.Context, _, user string) (string, error) {
		for kw, verdict := range byKeyword {
			if strings.Contains(user, kw) {
				return verdict, nil
			}
		}
		return "no", nil
	}
}

func TestRunSuite_PassingCase(t *testing.T) {
	cases := []GoldenCase{{
		Name: "wolt", Query: "wolt total?", MustCiteDocIDs: []string{"doc-1"},
	}}
	runner := func(_ context.Context, _ string) (TurnOutput, error) {
		return TurnOutput{Answer: "€8.40", CitedDocIDs: []string{"doc-1", "doc-2"}, Evidence: []string{"Wolt receipt €8.40"}}, nil
	}
	judge := scriptedJudge(map[string]string{
		"address the question": "yes", // relevance
		"hallucinated":         "yes", // faithfulness
	})
	rep := RunSuite(context.Background(), cases, runner, judge, "m", "j")
	r := rep.Results[0]
	if !r.Passed() {
		t.Fatalf("case should pass: %+v", r)
	}
	if r.CitedCorrect == nil || !*r.CitedCorrect {
		t.Errorf("cited should be correct")
	}
	if rep.PassCount() != 1 {
		t.Errorf("PassCount=%d", rep.PassCount())
	}
}

func TestRunSuite_MissingCitationFails(t *testing.T) {
	cases := []GoldenCase{{Name: "x", Query: "q", MustCiteDocIDs: []string{"need"}}}
	runner := func(_ context.Context, _ string) (TurnOutput, error) {
		return TurnOutput{Answer: "a", CitedDocIDs: []string{"other"}, Evidence: []string{"ctx"}}, nil
	}
	rep := RunSuite(context.Background(), cases, runner, scriptedJudge(map[string]string{"address": "yes", "hallucinated": "yes"}), "m", "j")
	if rep.Results[0].Passed() {
		t.Error("missing citation should fail the case")
	}
	if len(rep.Results[0].MissingCites) != 1 {
		t.Errorf("missing=%v", rep.Results[0].MissingCites)
	}
}

func TestRunSuite_AbstainCase(t *testing.T) {
	cases := []GoldenCase{{Name: "absent", Query: "what's my cat's name?", ShouldAbstain: true}}
	runner := func(_ context.Context, _ string) (TurnOutput, error) {
		return TurnOutput{Answer: "I couldn't find that in your data.", Evidence: nil}, nil
	}
	// abstainPrompt asks "could NOT find" → yes; relevance → partial.
	judge := scriptedJudge(map[string]string{"could NOT find": "yes", "address the question": "partial"})
	rep := RunSuite(context.Background(), cases, runner, judge, "m", "j")
	r := rep.Results[0]
	if r.Abstained == nil || !*r.Abstained {
		t.Errorf("should detect abstention: %+v", r)
	}
	if r.Faithful != nil {
		t.Errorf("abstain case should not run faithfulness")
	}
	if !r.Passed() {
		t.Errorf("abstain-and-abstained should pass: %+v", r)
	}
}

func TestRunSuite_TurnErrorRecorded(t *testing.T) {
	cases := []GoldenCase{{Name: "boom", Query: "q"}}
	runner := func(_ context.Context, _ string) (TurnOutput, error) {
		return TurnOutput{}, errors.New("backend down")
	}
	rep := RunSuite(context.Background(), cases, runner, scriptedJudge(nil), "m", "j")
	if rep.Results[0].Passed() || !strings.Contains(rep.Results[0].Error, "backend down") {
		t.Errorf("turn error not recorded: %+v", rep.Results[0])
	}
}

func TestRenderMarkdown_FlagsRegression(t *testing.T) {
	rep := Report{
		Model: "m", JudgeModel: "j",
		Results: []CaseResult{
			{Name: "good", Relevance: strPtr("yes")},
			{Name: "broke", Relevance: strPtr("no")}, // fails now
		},
	}
	prev := Baseline{"good": true, "broke": true} // broke used to pass
	md := RenderMarkdown(rep, prev)
	if !strings.Contains(md, "REGRESSED") || !strings.Contains(md, "broke") {
		t.Errorf("report should flag the regression:\n%s", md)
	}
	if !strings.Contains(md, "Passed: 1 / 2") {
		t.Errorf("summary wrong:\n%s", md)
	}
}

func TestBaselineRoundTrip(t *testing.T) {
	path := filepath.Join(t.TempDir(), "baseline.json")
	rep := Report{Results: []CaseResult{{Name: "a", Relevance: strPtr("yes")}, {Name: "b", Relevance: strPtr("no")}}}
	if err := SaveBaseline(path, rep); err != nil {
		t.Fatal(err)
	}
	b, err := LoadBaseline(path)
	if err != nil {
		t.Fatal(err)
	}
	if !b["a"] || b["b"] {
		t.Errorf("baseline = %+v", b)
	}
	// Missing file → empty, no error.
	empty, err := LoadBaseline(filepath.Join(t.TempDir(), "nope.json"))
	if err != nil || len(empty) != 0 {
		t.Errorf("missing baseline should be empty: %v %v", empty, err)
	}
}

func strPtr(s string) *string { return &s }
