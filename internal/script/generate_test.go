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
