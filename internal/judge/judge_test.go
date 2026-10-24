package judge

import (
	"context"
	"errors"
	"strings"
	"testing"

	"sleepy-agent/internal/llm"
)

func review(t *testing.T, j Judge) (Verdict, error) {
	t.Helper()
	return j.Review(context.Background(), "The Quiet Library", "Quiet Pages", "Pages turn slowly.")
}

func TestReviewPassesWhenNoScoreIsUnderTheMinimum(t *testing.T) {
	m := llm.NewMock(`{"calm": 5, "coherence": 3, "variety": 4, "narration": 4, "reason": "none"}`)
	v, err := review(t, Judge{Provider: m})
	if err != nil {
		t.Fatal(err)
	}
	if !v.Pass || v.Calm != 5 || v.Coherence != 3 || v.Variety != 4 || v.MinScore != 3 || v.Reason != "none" {
		t.Fatalf("%+v", v)
	}
}

func TestReviewRejectsWhenAnyScoreIsUnderTheMinimum(t *testing.T) {
	m := llm.NewMock(`{"calm": 5, "coherence": 2, "variety": 5, "narration": 5, "reason": "forest images in a library"}`)
	v, _ := review(t, Judge{Provider: m})
	if v.Pass {
		t.Fatalf("coherence 2 is under 3: %+v", v)
	}
	if v.Reason != "forest images in a library" {
		t.Errorf("reason %q", v.Reason)
	}
}

func TestThePassRuleIsCodeNotTheModelsOpinion(t *testing.T) {
	m := llm.NewMock(`{"calm": 1, "coherence": 1, "variety": 1, "narration": 1, "pass": true, "min_score": 1, "reason": "x"}`)
	v, _ := review(t, Judge{Provider: m})
	if v.Pass || v.MinScore != 3 {
		t.Fatalf("a model that says pass must not decide it: %+v", v)
	}
}

func TestCustomMinScore(t *testing.T) {
	m := llm.NewMock(`{"calm": 4, "coherence": 4, "variety": 4, "narration": 4, "reason": "none"}`)
	v, _ := review(t, Judge{Provider: m, MinScore: 5})
	if v.Pass || v.MinScore != 5 {
		t.Fatalf("%+v", v)
	}
}

func TestReviewReadsJSONInsideProse(t *testing.T) {
	m := llm.NewMock("Here is my review:\n```json\n{\"calm\": 4, \"coherence\": 4, \"variety\": 4, \"narration\": 4, \"reason\": \"none\"}\n```")
	if v, err := review(t, Judge{Provider: m}); err != nil || !v.Pass {
		t.Fatalf("%+v %v", v, err)
	}
}

func TestBadReplies(t *testing.T) {
	for name, reply := range map[string]string{
		"no json":        "I think it is fine.",
		"broken json":    `{"calm": 4,`,
		"score too high": `{"calm": 6, "coherence": 3, "variety": 3, "narration": 3, "reason": ""}`,
		"score zero":     `{"calm": 3, "coherence": 0, "variety": 3, "narration": 3, "reason": ""}`,
		"missing score":  `{"calm": 3, "coherence": 3, "reason": ""}`,
	} {
		_, err := review(t, Judge{Provider: llm.NewMock(reply)})
		if !errors.Is(err, ErrBadVerdict) {
			t.Errorf("%s: err = %v, want ErrBadVerdict", name, err)
		}
	}
}

func TestATruncatedReplyIsAnErrorNotAVerdict(t *testing.T) {
	m := llm.NewMockResponses(llm.Response{Content: `{"calm": 5`, FinishReason: "length", ReasoningTokens: 1990,
		Usage: llm.Usage{CompletionTokens: 2000}})
	_, err := review(t, Judge{Provider: m})
	if !errors.Is(err, ErrTruncated) || !strings.Contains(err.Error(), "1990 reasoning") {
		t.Fatalf("err = %v", err)
	}
}

func TestProviderErrorsPassThrough(t *testing.T) {
	m := llm.NewMock()
	boom := errors.New("boom")
	m.FailNext(boom)
	if _, err := review(t, Judge{Provider: m}); !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	if _, err := review(t, Judge{}); err == nil {
		t.Fatal("no provider must be an error")
	}
}

func TestTheRequestCarriesTheScriptAndAsksForJSON(t *testing.T) {
	m := llm.NewMock(`{"calm": 4, "coherence": 4, "variety": 4, "narration": 4, "reason": "none"}`)
	if _, err := review(t, Judge{Provider: m, MaxTokens: 1234}); err != nil {
		t.Fatal(err)
	}
	r := m.Requests()[0]
	if !r.JSON || r.MaxTokens != 1234 || r.Temperature != 0 {
		t.Errorf("request %+v", r)
	}
	user := r.Messages[1].Content
	for _, want := range []string{"Topic: The Quiet Library", "Title: Quiet Pages", "Pages turn slowly."} {
		if !strings.Contains(user, want) {
			t.Errorf("user message misses %q:\n%s", want, user)
		}
	}
	if def := (Judge{Provider: llm.NewMock(`{"calm":3,"coherence":3,"variety":3,"narration":3}`)}); true {
		_, _ = review(t, def)
		if got := def.Provider.(*llm.Mock).Requests()[0].MaxTokens; got != 2000 {
			t.Errorf("default budget %d", got)
		}
	}
}

func TestUsageIsRecorded(t *testing.T) {
	m := llm.NewMockResponses(llm.Response{Content: `{"calm": 4, "coherence": 4, "variety": 4, "narration": 4, "reason": "none"}`,
		Usage: llm.Usage{PromptTokens: 900, CompletionTokens: 120}})
	v, _ := review(t, Judge{Provider: m})
	if v.PromptTokens != 900 || v.CompletionTokens != 120 {
		t.Fatalf("%+v", v)
	}
}

func TestRubricV2ChangesOnlyTheCoherenceLine(t *testing.T) {
	v1, ok1 := PromptFor("")
	v1b, _ := PromptFor(RubricV1)
	v2, ok2 := PromptFor(RubricV2)
	if !ok1 || !ok2 || v1 != v1b || v1 == v2 {
		t.Fatal("v1 is the default and v2 must differ")
	}
	if !strings.Contains(v2, "one thread and one time frame, not one place") || strings.Contains(v1, "one time frame") {
		t.Fatal("only v2 carries the new coherence wording")
	}
	if strings.Replace(v2, v2[strings.Index(v2, " Coherence means"):strings.Index(v2, "disappears.")+len("disappears.")], "", 1) != v1 {
		t.Fatal("v2 must be v1 plus the one added sentence")
	}
	if _, ok := PromptFor("v9"); ok {
		t.Fatal("an unknown rubric must not resolve")
	}
}

func TestReviewUsesTheChosenRubricAndRejectsUnknownOnes(t *testing.T) {
	m := llm.NewMock(`{"calm":5,"coherence":5,"variety":5,"narration":5,"reason":"none","evidence":"none"}`, "x")
	if _, err := (Judge{Provider: m, Rubric: RubricV2}).Review(context.Background(), "t", "t", "body"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.Requests()[0].Messages[0].Content, "one thread and one time frame") {
		t.Fatal("v2 prompt was not sent")
	}
	if _, err := (Judge{Provider: m, Rubric: "v9"}).Review(context.Background(), "t", "t", "body"); err == nil {
		t.Fatal("unknown rubric must fail")
	}
}
