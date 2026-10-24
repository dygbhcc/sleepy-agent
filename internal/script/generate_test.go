package script

import (
	"context"
	"errors"
	"strings"
	"testing"

	"sleepy-agent/internal/llm"
)

func gen(t *testing.T, p llm.Provider, cfg Config) *Generator {
	t.Helper()
	g, err := New(p, cfg)
	if err != nil {
		t.Fatal(err)
	}
	return g
}

func TestFiveMinutesIsOneChunkAndATitle(t *testing.T) {
	m := llm.NewMock(cleanScript(12), `"Velvet Meadow"`)
	res, err := gen(t, m, Config{}).Generate(context.Background(), "a quiet meadow")
	if err != nil {
		t.Fatal(err)
	}
	if res.Chunks != 1 || res.Title != "Velvet Meadow" || res.TitleFallback || res.Body != cleanScript(12) {
		t.Fatalf("unexpected result %+v", res)
	}
	if got := len(m.Requests()); got != 2 {
		t.Fatalf("want 1 script call and 1 title call, got %d", got)
	}
	if res.Usage.PromptTokens == 0 || res.Usage.CompletionTokens == 0 {
		t.Fatalf("usage must be summed over both calls: %+v", res.Usage)
	}
}

func TestChunkPlan(t *testing.T) {
	cases := []struct{ target, paras, chunks int }{
		{650, 12, 1},  // 5 minutes: 650/56 = 11, raised to the 12 paragraph floor
		{1300, 23, 2}, // 10 minutes
		{3900, 69, 6}, // 30 minutes
		{0, 12, 1},
	}
	for _, c := range cases {
		p, n := plan(c.target)
		if p != c.paras || n != c.chunks {
			t.Fatalf("plan(%d) = %d paragraphs, %d chunks, want %d, %d", c.target, p, n, c.paras, c.chunks)
		}
	}
}

func TestSecondChunkContinuesFromTheLastTwoParagraphs(t *testing.T) {
	first, second := cleanScript(12), strings.ReplaceAll(cleanScript(11), "river", "lake")
	m := llm.NewMock(first, second, "Slow Moons")
	res, err := gen(t, m, Config{DurationMin: 10}).Generate(context.Background(), "moons")
	if err != nil {
		t.Fatal(err)
	}
	if res.Chunks != 2 || res.Body != first+"\n\n"+second {
		t.Fatalf("chunks %d, body joined wrongly", res.Chunks)
	}
	reqs := m.Requests()
	p1, p2 := reqs[0].Messages[1].Content, reqs[1].Messages[1].Content
	if !strings.Contains(p1, "Write exactly 12 paragraphs") || strings.Contains(p1, "PREVIOUS CONTEXT") {
		t.Fatalf("first chunk prompt wrong:\n%s", p1)
	}
	if !strings.Contains(p2, "Write exactly 11 paragraphs") || !strings.Contains(p2, "Do NOT repeat any existing text") {
		t.Fatalf("second chunk prompt wrong:\n%s", p2)
	}
	paras := splitParagraphs(first)
	if !strings.Contains(p2, paras[10]) || !strings.Contains(p2, paras[11]) || strings.Contains(p2, paras[9]) {
		t.Fatalf("second chunk must carry exactly the last two paragraphs of the first:\n%s", p2)
	}
	if !strings.Contains(p2, "End with the softest imagery and stillness") || strings.Contains(p1, "End with the softest") {
		t.Fatal("only the last chunk is told to end")
	}
}

func TestImageryRotatesBetweenChunks(t *testing.T) {
	m := llm.NewMock(cleanScript(12), cleanScript(11), "t")
	if _, err := gen(t, m, Config{DurationMin: 10, Style: "Cosmos"}).Generate(context.Background(), "x"); err != nil {
		t.Fatal(err)
	}
	reqs := m.Requests()
	one := strings.Index(reqs[0].Messages[1].Content, "1. Soft starlight")
	two := strings.Index(reqs[1].Messages[1].Content, "1. Soft starlight")
	if one < 0 || two >= 0 {
		t.Fatal("chunk 1 starts with the first imagery set, chunk 2 must start elsewhere")
	}
}

func TestRequestCarriesSleepysSettings(t *testing.T) {
	m := llm.NewMock(cleanScript(12), "t")
	_, _ = gen(t, m, Config{}).Generate(context.Background(), "x")
	req := m.Requests()[0]
	if req.Temperature != 0.7 || req.MaxTokens != 1500 || len(req.Tools) != 0 {
		t.Fatalf("unexpected request %+v", req)
	}
	if !strings.Contains(req.Messages[0].Content, "Zero exclamation marks") {
		t.Fatal("system prompt missing")
	}
}

func TestOutputIsNotPostProcessed(t *testing.T) {
	raw := "The sudden panic of the war came. Rest now!\n\n---SSML---\n<speak>x</speak>"
	m := llm.NewMock(raw, "t")
	res, err := gen(t, m, Config{}).Generate(context.Background(), "x")
	if err != nil {
		t.Fatal(err)
	}
	if res.Body != "The sudden panic of the war came. Rest now!" {
		t.Fatalf("only the SSML tail may be cut, banned words stay so they can be measured: %q", res.Body)
	}
}

func TestEmptyChunkFailsButKeepsUsage(t *testing.T) {
	_, err := gen(t, llm.NewMock("   \n "), Config{}).Generate(context.Background(), "x")
	if err == nil || !strings.Contains(err.Error(), "no script text") {
		t.Fatalf("want an empty chunk error, got %v", err)
	}
	m := llm.NewMock("  ")
	res, _ := gen(t, m, Config{}).Generate(context.Background(), "x")
	if res.Usage.PromptTokens == 0 {
		t.Fatal("usage of the failed call must be reported")
	}
}

func TestEmptyChunkErrorSaysWhyTheModelStopped(t *testing.T) {
	m := llm.NewMockResponses(llm.Response{Content: "", FinishReason: "length", ReasoningTokens: 1500,
		Usage: llm.Usage{PromptTokens: 90, CompletionTokens: 1500}})
	_, err := gen(t, m, Config{MaxTokensPerChunk: 1500}).Generate(context.Background(), "x")
	if err == nil {
		t.Fatal("want an error")
	}
	for _, want := range []string{`finish_reason "length"`, "1500 completion tokens", "1500 reasoning", "max_tokens 1500"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("error should contain %q: %v", want, err)
		}
	}
}

func TestATruncatedChunkIsAnErrorAndItsTextIsDropped(t *testing.T) {
	cut := "Morning dew gathers gently on cool grass. The scene settles into gentle harmony"
	m := llm.NewMockResponses(llm.Response{Content: cut, FinishReason: "length", ReasoningTokens: 1400,
		Usage: llm.Usage{PromptTokens: 90, CompletionTokens: 1500}})
	res, err := gen(t, m, Config{MaxTokensPerChunk: 1500}).Generate(context.Background(), "x")
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
	for _, want := range []string{"chunk 1 of 1", `finish_reason "length"`, "1500 completion tokens of which 1400 reasoning", "max_tokens 1500", "13 words"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error should contain %q: %v", want, err)
		}
	}
	if res.Body != "" || res.Chunks != 0 {
		t.Fatalf("the cut off text must not become a script: %+v", res)
	}
	if res.Usage.CompletionTokens != 1500 {
		t.Fatalf("usage of the failed call must be reported, got %+v", res.Usage)
	}
	if n := len(m.Requests()); n != 1 {
		t.Fatalf("no title call after a truncated script, got %d model calls", n)
	}
}

func TestAnEmptyReplyThatHitTheLimitIsTruncatedToo(t *testing.T) {
	// The gpt-oss failure: 1498 of 1500 completion tokens were reasoning, so no
	// text at all. It must be the same retryable error as a cut off script.
	m := llm.NewMockResponses(llm.Response{Content: "", FinishReason: "length", ReasoningTokens: 1498,
		Usage: llm.Usage{PromptTokens: 90, CompletionTokens: 1500}})
	_, err := gen(t, m, Config{MaxTokensPerChunk: 1500}).Generate(context.Background(), "x")
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
	if !strings.Contains(err.Error(), "no script text") || !strings.Contains(err.Error(), "1498 reasoning") {
		t.Fatalf("the message must say what happened: %v", err)
	}
	// An empty reply that did NOT hit the limit is a plain error.
	m = llm.NewMockResponses(llm.Response{Content: "  ", FinishReason: "stop"})
	_, err = gen(t, m, Config{}).Generate(context.Background(), "x")
	if err == nil || errors.Is(err, ErrTruncated) || !strings.Contains(err.Error(), "no script text") {
		t.Fatalf("want a plain empty reply error, got %v", err)
	}
}

func TestOnlyFinishReasonLengthMarksATruncatedChunk(t *testing.T) {
	for _, reason := range []string{"", "stop", "tool_calls"} {
		m := llm.NewMockResponses(llm.Response{Content: cleanScript(12), FinishReason: reason}, llm.Response{Content: "Velvet Meadow"})
		if _, err := gen(t, m, Config{}).Generate(context.Background(), "x"); err != nil {
			t.Errorf("finish_reason %q must be accepted, got %v", reason, err)
		}
	}
}

func TestTruncationInALaterChunkNamesThatChunk(t *testing.T) {
	m := llm.NewMockResponses(
		llm.Response{Content: cleanScript(12), FinishReason: "stop"},
		llm.Response{Content: "A line that stops", FinishReason: "length"},
	)
	res, err := gen(t, m, Config{DurationMin: 10}).Generate(context.Background(), "x")
	if !errors.Is(err, ErrTruncated) || !strings.Contains(err.Error(), "chunk 2 of 2") {
		t.Fatalf("got %v", err)
	}
	if res.Chunks != 1 {
		t.Fatalf("the finished first chunk is counted, got %d", res.Chunks)
	}
}

func TestProviderErrorIsWrappedAndUsageSoFarKept(t *testing.T) {
	m := llm.NewMock(cleanScript(12))
	boom := errors.New("down")
	res, err := gen(t, m, Config{DurationMin: 10}).Generate(context.Background(), "x")
	if !errors.Is(err, llm.ErrScriptExhausted) {
		t.Fatalf("want the provider error, got %v", err)
	}
	if res.Chunks != 1 || res.Usage.CompletionTokens == 0 {
		t.Fatalf("first chunk was paid for and must show: %+v", res)
	}
	m2 := llm.NewMock()
	m2.FailNext(boom)
	if _, err := gen(t, m2, Config{}).Generate(context.Background(), "x"); !errors.Is(err, boom) {
		t.Fatalf("got %v", err)
	}
}

func TestFailedTitleFallsBackToTheTopic(t *testing.T) {
	m := llm.NewMock(cleanScript(12)) // the title call hits an empty script
	res, err := gen(t, m, Config{}).Generate(context.Background(), "a quiet meadow")
	if err != nil {
		t.Fatalf("a finished script must survive a failed title: %v", err)
	}
	if res.Title != "a quiet meadow" || !res.TitleFallback {
		t.Fatalf("want the topic as fallback title, got %+v", res)
	}
}

func TestMultiLineTitleIsRejected(t *testing.T) {
	m := llm.NewMock(cleanScript(12), "Here are some options:\n1. Velvet Meadow")
	res, _ := gen(t, m, Config{}).Generate(context.Background(), "x")
	if !res.TitleFallback {
		t.Fatalf("a chatty title must not be used: %+v", res)
	}
}

func TestCancelledContextStops(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := gen(t, llm.NewMock(cleanScript(12)), Config{}).Generate(ctx, "x"); !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestNewRejectsBadConfig(t *testing.T) {
	if _, err := New(nil, Config{}); err == nil {
		t.Fatal("nil provider")
	}
	if _, err := New(llm.NewMock(), Config{Style: "Disco"}); err == nil {
		t.Fatal("unknown style")
	}
	if _, err := New(llm.NewMock(), Config{Language: "xx"}); err == nil {
		t.Fatal("unknown language")
	}
	if _, err := gen(t, llm.NewMock(), Config{}).Generate(context.Background(), "  "); err == nil {
		t.Fatal("empty topic")
	}
}

func TestTitleCallIsNotCappedAtAHundredTokensAndTruncationFallsBack(t *testing.T) {
	m := llm.NewMockResponses(
		llm.Response{Content: cleanScript(12), FinishReason: "stop"},
		llm.Response{Content: "Half a", FinishReason: "length", ReasoningTokens: 98},
	)
	res, err := gen(t, m, Config{MaxTokensPerChunk: 4000}).Generate(context.Background(), "A Lighthouse")
	if err != nil {
		t.Fatalf("a truncated title must not lose the script: %v", err)
	}
	if !res.TitleFallback || res.Title != "A Lighthouse" {
		t.Fatalf("want the topic as flagged fallback, got %q fallback=%v", res.Title, res.TitleFallback)
	}
	reqs := m.Requests()
	if got := reqs[len(reqs)-1].MaxTokens; got != 4000 {
		t.Fatalf("title MaxTokens = %d, want the configured 4000", got)
	}
}
