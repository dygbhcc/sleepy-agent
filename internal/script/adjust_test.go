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
		"imagery":     {Imagery: ImageryTopic},
	} {
		if gen(t, llm.NewMock(), cfg).Fingerprint() == base {
			t.Errorf("changing the %s must change the fingerprint", name)
		}
	}
	if !strings.Contains(base, PromptVersion) || !strings.Contains(base, "mock") {
		t.Errorf("fingerprint %q should name the model and the prompt version", base)
	}
}

func TestARunStyleOverridesTheConfiguredStyle(t *testing.T) {
	m := llm.NewMock(cleanScript(12), "Rings of Saturn")
	_, err := gen(t, m, Config{Style: "Earthside"}).GenerateAdjusted(context.Background(), "Rings of Saturn", Adjust{Style: "Cosmos"})
	if err != nil {
		t.Fatal(err)
	}
	reqs := m.Requests()
	if !strings.Contains(reqs[0].Messages[1].Content, "1. Soft starlight") {
		t.Fatal("the chunk prompt must carry the Cosmos imagery, not the configured Earthside set")
	}
	if !strings.Contains(reqs[1].Messages[1].Content, "Style: Cosmos") {
		t.Fatalf("the title call must see the run's style: %q", reqs[1].Messages[1].Content)
	}
}

func TestNoRunStyleKeepsTheConfiguredStyle(t *testing.T) {
	m := llm.NewMock(cleanScript(12), "t")
	if _, err := gen(t, m, Config{Style: "Myth"}).GenerateAdjusted(context.Background(), "x", Adjust{}); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.Requests()[1].Messages[1].Content, "Style: Myth") {
		t.Fatal("an empty run style must fall back to the configured one")
	}
}

func TestAnUnknownRunStyleIsRejectedBeforeAnyModelCall(t *testing.T) {
	for _, mode := range []Mode{ModeRegenerate, ModeContinue, ModeRetitle} {
		m := llm.NewMock(cleanScript(12), "t")
		a := Adjust{Mode: mode, Style: "Noir", PreviousBody: "A body."}
		_, err := gen(t, m, Config{}).GenerateAdjusted(context.Background(), "x", a)
		if err == nil || !strings.Contains(err.Error(), "Noir") {
			t.Fatalf("mode %q: err = %v, want an unknown style error", mode, err)
		}
		if n := len(m.Requests()); n != 0 {
			t.Fatalf("mode %q made %d model calls for a style it cannot use", mode, n)
		}
	}
}

func TestRetitleUsesTheRunStyle(t *testing.T) {
	m := llm.NewMock("New Name")
	a := Adjust{Mode: ModeRetitle, Style: "Cosmos", PreviousBody: "A body."}
	if _, err := gen(t, m, Config{}).GenerateAdjusted(context.Background(), "x", a); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(m.Requests()[0].Messages[1].Content, "Style: Cosmos") {
		t.Fatal("a retitle must use the run's style")
	}
}

func TestStylesListsTheKnownOnes(t *testing.T) {
	if got := strings.Join(Styles(), ","); got != "Cosmos,Earthside,Myth" {
		t.Fatalf("Styles() = %q", got)
	}
}

func TestThePromptAsksForTheTopicAndForParaphrasedImagery(t *testing.T) {
	m := llm.NewMock(cleanScript(12), "Rings of Saturn")
	if _, err := gen(t, m, Config{}).Generate(context.Background(), "Rings of Saturn"); err != nil {
		t.Fatal(err)
	}
	reqs := m.Requests()
	chunk := reqs[0].Messages[1].Content
	for _, want := range []string{
		"This episode is about: Rings of Saturn.",
		"Let the whole script take place in the world of this topic.",
		"Do not begin sentences with the topic's name",
		`never talks about "the episode"`,
		"never copy their wording",
	} {
		if !strings.Contains(chunk, want) {
			t.Errorf("chunk prompt lacks %q", want)
		}
	}
	if !strings.Contains(reqs[1].Messages[0].Content, "keep its main word or words in the name") {
		t.Error("the title prompt must ask the name to keep the topic")
	}
}

func TestThePromptNeverForcesTheTopicIntoAParagraph(t *testing.T) {
	m := llm.NewMock(cleanScript(12), cleanScript(11), "t")
	if _, err := gen(t, m, Config{DurationMin: 10}).Generate(context.Background(), "Rings of Saturn"); err != nil {
		t.Fatal(err)
	}
	for i, r := range m.Requests()[:2] {
		p := r.Messages[1].Content
		for _, banned := range []string{"Name the topic", "opening paragraph", "closing paragraph", "Come back to it"} {
			if strings.Contains(p, banned) {
				t.Errorf("chunk %d tells the model where to mention the topic: %q", i+1, banned)
			}
		}
	}
}

func TestTopicImageryMode(t *testing.T) {
	stock := llm.NewMock(cleanScript(12), "t")
	if _, err := gen(t, stock, Config{Style: "Earthside"}).Generate(context.Background(), "A Lighthouse at Dusk"); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(stock.Requests()[0].Messages[1].Content, "1. Dew forming on cool morning grass.") {
		t.Fatal("the default mode gives the model the stock lines")
	}
	if !strings.Contains(stock.Requests()[0].Messages[1].Content, "rotate through them") {
		t.Fatal("the stock prompt is unchanged")
	}

	topic := llm.NewMock(cleanScript(12), "t")
	if _, err := gen(t, topic, Config{Style: "Earthside", Imagery: ImageryTopic}).Generate(context.Background(), "A Lighthouse at Dusk"); err != nil {
		t.Fatal(err)
	}
	p := topic.Requests()[0].Messages[1].Content
	if strings.Contains(p, "Dew forming") || strings.Contains(p, "rotate through them") {
		t.Fatalf("topic mode must not carry the stock lines:\n%s", p)
	}
	if !strings.Contains(p, "Every image comes from the world of the topic.") || !strings.Contains(p, "This episode is about: A Lighthouse at Dusk.") {
		t.Fatalf("topic mode asks for the topic's own images:\n%s", p)
	}
	if !strings.Contains(p, "STYLE:") || !strings.Contains(p, "Imagery of quiet forests") {
		t.Fatal("the style description stays")
	}
}

func TestUnknownImageryModeIsRejected(t *testing.T) {
	if _, err := New(llm.NewMock(), Config{Imagery: "random"}); err == nil || !strings.Contains(err.Error(), "random") {
		t.Fatalf("err = %v", err)
	}
}

func TestEveryStyleHasOneAnchorPerImageryLine(t *testing.T) {
	for _, style := range Styles() {
		if a, l := len(ImageryAnchors(style)), len(ImageryLines(style)); a != l || a == 0 {
			t.Errorf("%s: %d anchors for %d lines", style, a, l)
		}
	}
}

func TestEachAnchorAppearsInItsOwnLine(t *testing.T) {
	for _, style := range Styles() {
		lines, anchors := ImageryLines(style), ImageryAnchors(style)
		for i, a := range anchors {
			if !strings.Contains(strings.ToLower(lines[i]), a) {
				t.Errorf("%s line %d %q does not contain its anchor %q", style, i+1, lines[i], a)
			}
		}
	}
}

func TestStrictTopicModeAlsoDropsTheStylesOwnImagery(t *testing.T) {
	m := llm.NewMock(cleanScript(12), "t")
	if _, err := gen(t, m, Config{Style: "Earthside", Imagery: ImageryTopicStrict}).Generate(context.Background(), "A Lighthouse at Dusk"); err != nil {
		t.Fatal(err)
	}
	p := m.Requests()[0].Messages[1].Content
	for _, gone := range []string{"Dew forming", "rotate through them", "quiet forests", "moss-covered stones"} {
		if strings.Contains(p, gone) {
			t.Errorf("strict topic mode still has %q", gone)
		}
	}
	if !strings.Contains(p, "A natural, earthly mood") || !strings.Contains(p, "Every image comes from the world of the topic.") {
		t.Fatalf("strict mode keeps a mood and asks for the topic's world:\n%s", p)
	}
	// The plain topic mode keeps the style description, so the two differ.
	m2 := llm.NewMock(cleanScript(12), "t")
	_, _ = gen(t, m2, Config{Style: "Earthside", Imagery: ImageryTopic}).Generate(context.Background(), "x")
	if !strings.Contains(m2.Requests()[0].Messages[1].Content, "quiet forests") {
		t.Fatal("plain topic mode keeps the style description")
	}
}

func TestEveryStyleHasAMood(t *testing.T) {
	for _, s := range Styles() {
		if styleMoods[s] == "" {
			t.Errorf("no mood for %s", s)
		}
	}
}
