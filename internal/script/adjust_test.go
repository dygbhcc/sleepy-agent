package script

import (
	"context"
	"errors"
	"strings"
	"testing"

	"sleepy-agent/internal/llm"
)

// shortBody is 4 paragraphs of 48 words, 192 words in all.
func shortBody() string { return cleanScript(4) }

func TestContinueKeepsTheOldTextAndWritesOnlyWhatIsMissing(t *testing.T) {
	old := shortBody()
	more := strings.ReplaceAll(cleanScript(9), "river", "lake")
	m := llm.NewMock(more)
	g := gen(t, m, Config{})

	// 650 target, 192 words kept, 458 missing: 9 paragraphs of about 56 words.
	res, err := g.GenerateAdjusted(context.Background(), "meadow", Adjust{
		Mode: ModeContinue, PreviousBody: old, PreviousTitle: "Velvet Meadow",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Body != old+"\n\n"+more {
		t.Fatal("the continuation must be appended to the old script, unchanged")
	}
	if res.Title != "Velvet Meadow" || res.Chunks != 1 {
		t.Fatalf("title %q chunks %d", res.Title, res.Chunks)
	}
	reqs := m.Requests()
	if len(reqs) != 1 {
		t.Fatalf("want one model call (no title call, the title was fine), got %d", len(reqs))
	}
	p := reqs[0].Messages[1].Content
	for _, want := range []string{"Write exactly 9 paragraphs", "Do NOT repeat any existing text", "PREVIOUS CONTEXT",
		splitParagraphs(old)[2], splitParagraphs(old)[3], "End with the softest imagery"} {
		if !strings.Contains(p, want) {
			t.Errorf("prompt should contain %q:\n%s", want, p)
		}
	}
	if strings.Contains(p, splitParagraphs(old)[1]) {
		t.Error("only the last two paragraphs of the old script are context")
	}
	if strings.Contains(p, "Begin extremely gentle") {
		t.Error("a continuation is not a beginning")
	}
}

func TestContinueWithoutAPreviousTitleAsksForOne(t *testing.T) {
	m := llm.NewMock(cleanScript(9), "Soft Lake")
	res, err := gen(t, m, Config{}).GenerateAdjusted(context.Background(), "t",
		Adjust{Mode: ModeContinue, PreviousBody: shortBody()})
	if err != nil || res.Title != "Soft Lake" || len(m.Requests()) != 2 {
		t.Fatalf("%+v %v, calls %d", res, err, len(m.Requests()))
	}
}

func TestContinueAlwaysAsksForSomethingEvenWhenTheTargetIsAlreadyMet(t *testing.T) {
	m := llm.NewMock(cleanScript(1), "Title")
	_, err := gen(t, m, Config{}).GenerateAdjusted(context.Background(), "t",
		Adjust{Mode: ModeContinue, PreviousBody: cleanScript(14), TargetWords: 100})
	if err != nil {
		t.Fatal(err)
	}
	if p := m.Requests()[0].Messages[1].Content; !strings.Contains(p, "Write exactly 1 paragraphs") {
		t.Fatalf("one paragraph is the least a continuation asks for:\n%s", p)
	}
}

func TestContinueSplitsIntoChunksWhenALotIsMissing(t *testing.T) {
	m := llm.NewMock(cleanScript(12), cleanScript(2), "Title")
	// 1300 target, 192 kept: 1108 missing is 20 paragraphs, so 12 and 8.
	_, err := gen(t, m, Config{}).GenerateAdjusted(context.Background(), "t",
		Adjust{Mode: ModeContinue, PreviousBody: shortBody(), TargetWords: 1300})
	if err != nil {
		t.Fatal(err)
	}
	reqs := m.Requests()
	if !strings.Contains(reqs[0].Messages[1].Content, "Write exactly 12 paragraphs") ||
		!strings.Contains(reqs[1].Messages[1].Content, "Write exactly 8 paragraphs") {
		t.Fatalf("chunk sizes wrong:\n%s\n---\n%s", reqs[0].Messages[1].Content, reqs[1].Messages[1].Content)
	}
	if strings.Contains(reqs[0].Messages[1].Content, "End with the softest") || !strings.Contains(reqs[1].Messages[1].Content, "End with the softest") {
		t.Fatal("only the last chunk of the continuation is told to end")
	}
}

func TestRegenerateUsesTheTargetTheTemperatureAndTheRepairNote(t *testing.T) {
	m := llm.NewMock(cleanScript(12), cleanScript(5), "Title")
	_, err := gen(t, m, Config{}).GenerateAdjusted(context.Background(), "t", Adjust{
		TargetWords: 1000, Temperature: 0.3, ExtraInstruction: "Do not use the word disappear.",
	})
	if err != nil {
		t.Fatal(err)
	}
	req := m.Requests()[0]
	if req.Temperature != 0.3 {
		t.Fatalf("temperature = %v, want 0.3", req.Temperature)
	}
	p := req.Messages[1].Content
	if !strings.Contains(p, "REPAIR NOTE") || !strings.Contains(p, "Do not use the word disappear.") {
		t.Fatalf("repair note missing:\n%s", p)
	}
	if strings.Count(p, "REPAIR NOTE") != 1 {
		t.Fatal("one note per prompt")
	}
	// Every chunk carries the note, not only the first.
	if second := m.Requests()[1]; !strings.Contains(second.Messages[1].Content, "Do not use the word disappear.") || second.Temperature != 0.3 {
		t.Fatalf("the second chunk must be steered too: temp %v", second.Temperature)
	}
}

func TestRegenerateTargetChangesTheNumberOfChunks(t *testing.T) {
	m := llm.NewMock(cleanScript(12), cleanScript(5), "Title")
	_, err := gen(t, m, Config{}).GenerateAdjusted(context.Background(), "t", Adjust{TargetWords: 1000})
	if err != nil {
		t.Fatal(err)
	}
	if got := len(m.Requests()); got != 3 {
		t.Fatalf("1000 words is 17 paragraphs, two chunks and a title, got %d calls", got)
	}
}

func TestAFirstAttemptPromptHasNoRepairNoteAndTheUsualTemperature(t *testing.T) {
	m := llm.NewMock(cleanScript(12), "Title")
	if _, err := gen(t, m, Config{}).Generate(context.Background(), "t"); err != nil {
		t.Fatal(err)
	}
	req := m.Requests()[0]
	if strings.Contains(req.Messages[1].Content, "REPAIR NOTE") || req.Temperature != 0.7 {
		t.Fatalf("a plain attempt must not change: temp %v\n%s", req.Temperature, req.Messages[1].Content)
	}
}

func TestRetitleKeepsTheScriptAndMakesOneCall(t *testing.T) {
	m := llm.NewMock(`"Amber Lanterns"`)
	body := cleanScript(12)
	res, err := gen(t, m, Config{}).GenerateAdjusted(context.Background(), "t", Adjust{
		Mode: ModeRetitle, PreviousBody: body, PreviousTitle: "WAR", Temperature: 0.6,
		ExtraInstruction: "The previous title was rejected: title is ALL CAPS.",
	})
	if err != nil {
		t.Fatal(err)
	}
	if res.Title != "Amber Lanterns" || res.Body != body || res.Chunks != 0 {
		t.Fatalf("%+v", res)
	}
	reqs := m.Requests()
	if len(reqs) != 1 {
		t.Fatalf("retitle is one call, got %d", len(reqs))
	}
	if reqs[0].Temperature != 0.6 || !strings.Contains(reqs[0].Messages[0].Content, "title is ALL CAPS") {
		t.Fatalf("temperature %v, system prompt:\n%s", reqs[0].Temperature, reqs[0].Messages[0].Content)
	}
}

func TestRetitleThatFailsKeepsTheScriptAndThePreviousTitle(t *testing.T) {
	m := llm.NewMock("")
	res, err := gen(t, m, Config{}).GenerateAdjusted(context.Background(), "t",
		Adjust{Mode: ModeRetitle, PreviousBody: "body", PreviousTitle: "Old"})
	if err != nil || res.Title != "Old" || !res.TitleFallback || res.Body != "body" {
		t.Fatalf("%+v %v", res, err)
	}
}

func TestAdjustedModesNeedAPreviousScriptAndKnownModes(t *testing.T) {
	g := gen(t, llm.NewMock(), Config{})
	for _, m := range []Mode{ModeContinue, ModeRetitle} {
		if _, err := g.GenerateAdjusted(context.Background(), "t", Adjust{Mode: m}); err == nil || !strings.Contains(err.Error(), "previous script") {
			t.Errorf("mode %q without a script: %v", m, err)
		}
	}
	if _, err := g.GenerateAdjusted(context.Background(), "t", Adjust{Mode: "polish", PreviousBody: "x"}); err == nil {
		t.Error("unknown mode must be refused")
	}
}

func TestATruncatedContinuationIsATruncationAndKeepsNothing(t *testing.T) {
	m := llm.NewMockResponses(llm.Response{Content: "The lake settles", FinishReason: "length"})
	res, err := gen(t, m, Config{}).GenerateAdjusted(context.Background(), "t",
		Adjust{Mode: ModeContinue, PreviousBody: shortBody(), PreviousTitle: "T"})
	if !errors.Is(err, ErrTruncated) || res.Body != "" {
		t.Fatalf("want ErrTruncated and no body, got %v, %q", err, res.Body)
	}
}

func TestFingerprintChangesWithEverythingThatChangesTheOutput(t *testing.T) {
	base := gen(t, llm.NewMock(), Config{}).Fingerprint()
	if base != gen(t, llm.NewMock(), Config{}).Fingerprint() {
		t.Fatal("the same setup must give the same fingerprint")
	}
	for name, cfg := range map[string]Config{
		"style":       {Style: "Cosmos"},
		"language":    {Language: "tr"},
		"duration":    {DurationMin: 10},
		"temperature": {Temperature: 0.9},
		"max tokens":  {MaxTokensPerChunk: 4000},
		"series":      {Series: "Other"},
	} {
		if gen(t, llm.NewMock(), cfg).Fingerprint() == base {
			t.Errorf("changing the %s must change the fingerprint", name)
		}
	}
	if !strings.Contains(base, PromptVersion) || !strings.Contains(base, "mock") {
		t.Errorf("fingerprint %q should name the model and the prompt version", base)
	}
}
