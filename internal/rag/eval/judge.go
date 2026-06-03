package eval

import (
	"context"
	"fmt"
	"strings"
)

const judgeSystem = "You are a strict evaluator of a retrieval-augmented assistant. " +
	"Follow the instruction exactly and reply with ONLY the requested single word — no explanation."

// parseVerdict normalises a judge reply to "yes" | "no" | "partial" | "".
// It reads the first meaningful token so a model that adds stray prose
// after the verdict still parses.
func parseVerdict(reply string) string {
	s := strings.ToLower(strings.TrimSpace(reply))
	s = strings.TrimLeft(s, "*-#> \t\n")
	switch {
	case strings.HasPrefix(s, "yes"):
		return "yes"
	case strings.HasPrefix(s, "partial"):
		return "partial"
	case strings.HasPrefix(s, "no"):
		return "no"
	}
	// Fall back to a contains-scan for "partial" before "no" (since
	// "partial" can't be confused, but a leading "no" already returned).
	if strings.Contains(s, "partial") {
		return "partial"
	}
	if strings.Contains(s, "yes") {
		return "yes"
	}
	if strings.Contains(s, "no") {
		return "no"
	}
	return ""
}

func relevancePrompt(query, answer string) string {
	return fmt.Sprintf(
		"Question:\n%s\n\nAssistant answer:\n%s\n\n"+
			"Does the answer address the question? Reply with one word: yes, no, or partial.",
		query, answer)
}

func faithfulnessPrompt(answer string, evidence []string) string {
	ctx := strings.Join(evidence, "\n---\n")
	if ctx == "" {
		ctx = "(no documents were retrieved)"
	}
	return fmt.Sprintf(
		"Retrieved documents:\n%s\n\nAssistant answer:\n%s\n\n"+
			"Is every factual claim in the answer supported by at least one of the "+
			"retrieved documents (i.e. not hallucinated)? Reply with one word: yes or no.",
		ctx, answer)
}

func abstainPrompt(answer string) string {
	return fmt.Sprintf(
		"Assistant answer:\n%s\n\n"+
			"Does the answer state that it could NOT find the information / does not "+
			"have enough information to answer (rather than making up an answer)? "+
			"Reply with one word: yes or no.",
		answer)
}

// RunSuite runs every case through the runner and scores it. Judge calls
// go through judgeGen. A turn error records the case as failed and skips
// the judge for that case; a judge error leaves the corresponding verdict
// nil (unscored) rather than failing the whole run.
func RunSuite(ctx context.Context, cases []GoldenCase, runner TurnRunner, judgeGen GenerateFunc, model, judgeModel string) Report {
	rep := Report{Model: model, JudgeModel: judgeModel}
	for _, c := range cases {
		rep.Results = append(rep.Results, scoreCase(ctx, c, runner, judgeGen))
	}
	return rep
}

func scoreCase(ctx context.Context, c GoldenCase, runner TurnRunner, judgeGen GenerateFunc) CaseResult {
	res := CaseResult{Name: c.Name, Lang: c.Lang, Query: c.Query}
	out, err := runner(ctx, c.Query)
	if err != nil {
		res.Error = "turn: " + err.Error()
		return res
	}
	res.Answer = out.Answer

	if len(c.MustCiteDocIDs) > 0 {
		ok, missing := citedCorrect(c.MustCiteDocIDs, out.CitedDocIDs)
		res.CitedCorrect = boolPtr(ok)
		res.MissingCites = missing
	}

	// Relevance always applies.
	if v, err := judgeGen(ctx, judgeSystem, relevancePrompt(c.Query, out.Answer)); err == nil {
		verdict := parseVerdict(v)
		if verdict != "" {
			res.Relevance = &verdict
		}
	} else {
		res.Error = "judge relevance: " + err.Error()
	}

	if c.ShouldAbstain {
		// The right behaviour is to decline, so judge abstention instead
		// of faithfulness (there's nothing it should have cited).
		if v, err := judgeGen(ctx, judgeSystem, abstainPrompt(out.Answer)); err == nil {
			res.Abstained = boolPtr(parseVerdict(v) == "yes")
		}
	} else if len(out.Evidence) > 0 {
		if v, err := judgeGen(ctx, judgeSystem, faithfulnessPrompt(out.Answer, out.Evidence)); err == nil {
			res.Faithful = boolPtr(parseVerdict(v) == "yes")
		}
	}
	return res
}
