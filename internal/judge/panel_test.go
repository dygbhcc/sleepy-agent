package judge

import (
	"context"
	"errors"
	"strings"
	"testing"

	"sleepy-agent/internal/llm"
)

func scoreJSON(calm, coh, vari, narr int, reason, evidence string) string {
	return `{"calm": ` + itoa(calm) + `, "coherence": ` + itoa(coh) + `, "variety": ` + itoa(vari) +
		`, "narration": ` + itoa(narr) + `, "reason": "` + reason + `", "evidence": "` + evidence + `"}`
}

func itoa(n int) string { return string(rune('0' + n)) } // scores are 0 to 9

func member(name, reply string) Member {
	return Member{Name: name, Reviewer: Judge{Provider: llm.NewMock(reply)}}
}

type failingReviewer struct{}

func (failingReviewer) Review(context.Context, string, string, string) (Verdict, error) {
	return Verdict{}, errors.New("provider down")
}

func TestNarrationCountsTowardThePassRule(t *testing.T) {
	v, err := Judge{Provider: llm.NewMock(scoreJSON(5, 5, 5, 2, "reads like an advertisement", "Buy now"))}.Review(context.Background(), "t", "T", "b")
	if err != nil || v.Pass {
		t.Fatalf("a low narration score must reject: %+v, %v", v, err)
	}
	if v.Evidence != "Buy now" || v.Narration != 2 {
		t.Fatalf("evidence and narration kept: %+v", v)
	}
}

func TestAPanelPassesOnlyWhenEveryMemberPasses(t *testing.T) {
	p := Panel{Members: []Member{member("a", scoreJSON(5, 5, 5, 5, "none", "none")), member("b", scoreJSON(4, 4, 4, 4, "none", "none"))}}
	v, err := p.Review(context.Background(), "t", "T", "b")
	if err != nil || !v.Pass {
		t.Fatalf("both pass, got %+v, %v", v, err)
	}
	if v.Calm != 4 || v.Narration != 4 {
		t.Fatalf("scores are the lowest any member gave: %+v", v)
	}
}

func TestOneRejectingMemberRejectsAndIsNamed(t *testing.T) {
	p := Panel{Members: []Member{
		member("gpt", scoreJSON(5, 5, 5, 5, "none", "none")),
		member("llama", scoreJSON(5, 2, 5, 5, "drifts to a forest", "The river bends")),
	}}
	v, err := p.Review(context.Background(), "t", "T", "b")
	if err != nil || v.Pass {
		t.Fatalf("one rejection rejects: %+v, %v", v, err)
	}
	if !strings.Contains(v.Reason, "llama: drifts to a forest") || strings.Contains(v.Reason, "gpt") {
		t.Fatalf("only the rejecting member is named: %q", v.Reason)
	}
	if !strings.Contains(v.Evidence, "llama: The river bends") {
		t.Fatalf("evidence: %q", v.Evidence)
	}
}

func TestAMemberThatCannotAnswerMakesTheWholePanelAnError(t *testing.T) {
	p := Panel{Members: []Member{member("a", scoreJSON(5, 5, 5, 5, "none", "none")), {Name: "down", Reviewer: failingReviewer{}}}}
	if _, err := p.Review(context.Background(), "t", "T", "b"); err == nil || !strings.Contains(err.Error(), "down: provider down") {
		t.Fatalf("an unreachable member must not count as a pass: %v", err)
	}
}

func TestReviewEachAsksEveryoneEvenAfterARejection(t *testing.T) {
	p := Panel{Members: []Member{
		member("a", scoreJSON(1, 1, 1, 1, "bad", "x")),
		member("b", scoreJSON(5, 5, 5, 5, "none", "none")),
		{Name: "c", Reviewer: failingReviewer{}},
	}}
	rs := p.ReviewEach(context.Background(), "t", "T", "b")
	if len(rs) != 3 || rs[0].Verdict.Pass || !rs[1].Verdict.Pass || rs[2].Err == nil {
		t.Fatalf("results %+v", rs)
	}
}

func TestAnEmptyPanelIsAnError(t *testing.T) {
	if _, err := (Panel{}).Review(context.Background(), "t", "T", "b"); err == nil {
		t.Fatal("no members, no verdict")
	}
}

func TestCombineSumsTokens(t *testing.T) {
	a := Verdict{Calm: 5, Coherence: 5, Variety: 5, Narration: 5, Pass: true, MinScore: 3, PromptTokens: 100, CompletionTokens: 10}
	b := Verdict{Calm: 5, Coherence: 5, Variety: 5, Narration: 5, Pass: true, MinScore: 3, PromptTokens: 200, CompletionTokens: 20}
	v, err := Combine([]Result{{Name: "a", Verdict: a}, {Name: "b", Verdict: b}})
	if err != nil || v.PromptTokens != 300 || v.CompletionTokens != 30 {
		t.Fatalf("%+v, %v", v, err)
	}
	if one, _ := Combine([]Result{{Name: "a", Verdict: a}}); one.PromptTokens != 100 {
		t.Fatalf("a panel of one keeps its tokens: %+v", one)
	}
}
