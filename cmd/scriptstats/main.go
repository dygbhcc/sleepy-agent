// Command scriptstats measures how well a model follows Sleepy's script
// rules when nothing checks its work.
//
// It generates RUNS scripts (default 5) with the generator ported from Sleepy,
// measures each one against the rules in the prompt, and prints the raw
// failure rate. There is no QA gate and no retry on bad output: the numbers
// are what the model does on its own. Day 6 adds the gates.
//
// Without GROQ_API_KEY it uses scripted mocks, which only proves the harness
// works. With a key, GROQ_MODEL is required (there is no default model).
//
// Every script is written to out/scriptstats/ so you can read it. The first
// real failures are usually found by reading.
package main

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"sleepy-agent/internal/llm"
	"sleepy-agent/internal/policy"
	"sleepy-agent/internal/script"
)

var topics = []string{
	"Rings of Saturn",
	"Velvet Meadow",
	"The Slow Moons",
	"Amber Lanterns",
	"Moss and Still Water",
	"A Lighthouse at Dusk",
	"Snow on Sleeping Fields",
	"The Quiet Library",
}

type summary struct {
	runs, errors, generated, clean int
	titleFallbacks                 int
	violations                     map[string]int
	words, prompt, completion      int
	retries                        int
	firstErr                       string
}

func main() {
	runs := envInt("RUNS", 5, 1, 50)
	duration := envInt("DURATION", 5, 1, 60)
	style := os.Getenv("STYLE")
	if style == "" {
		style = "Earthside"
	}

	build, label := source()
	cfg := script.Config{Style: style, DurationMin: duration}
	fmt.Printf("model: %s, style %s, %d minute episodes, %d runs\n", label, style, duration, runs)
	fmt.Printf("pause between runs: %s\n\n", pause())

	sum := summary{runs: runs, violations: map[string]int{}}
	outDir := filepath.Join("out", "scriptstats")
	_ = os.MkdirAll(outDir, 0o755)

	for i := 1; i <= runs; i++ {
		if i > 1 {
			time.Sleep(pause())
		}
		topic := topics[(i-1)%len(topics)]
		p := build(i, &sum.retries)
		g, err := script.New(p, cfg)
		if err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(2)
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		res, err := g.Generate(ctx, topic)
		cancel()

		sum.prompt += res.Usage.PromptTokens
		sum.completion += res.Usage.CompletionTokens
		if err != nil {
			sum.errors++
			msg := oneLine(err.Error())
			if sum.firstErr == "" {
				sum.firstErr = msg
			}
			fmt.Printf("run %d  %-26s ERROR %s\n", i, topic, msg)
			continue
		}

		rep := script.Measure(res.Body, g.Policy())
		sum.generated++
		sum.words += rep.Words
		if rep.Clean() {
			sum.clean++
		}
		for _, v := range rep.Violations {
			sum.violations[v]++
		}
		if res.TitleFallback {
			sum.titleFallbacks++
		}
		verdict := "clean"
		if !rep.Clean() {
			verdict = strings.Join(rep.Violations, ", ")
		}
		fmt.Printf("run %d  %-26s %q  %d words  %s\n", i, topic, res.Title, rep.Words, verdict)
		if len(rep.BannedFound) > 0 {
			fmt.Printf("        banned words used: %s\n", strings.Join(rep.BannedFound, ", "))
		}
		save(outDir, i, topic, res)
	}
	print(sum, cfg)
}

func print(s summary, cfg script.Config) {
	fmt.Println()
	fmt.Println("| runs | generation errors | generated | clean | avg words | avg tokens (prompt+completion) | rate-limit retries |")
	fmt.Println("|---|---|---|---|---|---|---|")
	avgWords, avgTokens := 0, 0
	if s.generated > 0 {
		avgWords = s.words / s.generated
	}
	if s.runs > 0 {
		avgTokens = (s.prompt + s.completion) / s.runs
	}
	fmt.Printf("| %d | %d | %d | %d/%d | %d | %d | %d |\n", s.runs, s.errors, s.generated, s.clean, s.generated, avgWords, avgTokens, s.retries)

	if len(s.violations) > 0 {
		fmt.Println("\n| broken rule | scripts |")
		fmt.Println("|---|---|")
		names := make([]string, 0, len(s.violations))
		for n := range s.violations {
			names = append(names, n)
		}
		sort.Slice(names, func(i, j int) bool {
			if s.violations[names[i]] != s.violations[names[j]] {
				return s.violations[names[i]] > s.violations[names[j]]
			}
			return names[i] < names[j]
		})
		for _, n := range names {
			fmt.Printf("| %s | %d/%d |\n", n, s.violations[n], s.generated)
		}
	}
	if s.titleFallbacks > 0 {
		fmt.Printf("\ntitle calls that failed and fell back to the topic: %d\n", s.titleFallbacks)
	}
	if s.firstErr != "" {
		fmt.Printf("\nfirst error: %s\n", s.firstErr)
	}
	limits := policy.ForDuration(cfg.DurationMin)
	fmt.Printf("\nword limits for %d minutes: %d to %d. Scripts are in out/scriptstats/.\n",
		cfg.DurationMin, limits.MinWords, limits.MaxWords)
}

func save(dir string, i int, topic string, res script.Result) {
	body := fmt.Sprintf("title: %s\ntopic: %s\n\n%s\n", res.Title, topic, res.Body)
	_ = os.WriteFile(filepath.Join(dir, fmt.Sprintf("run-%d.md", i)), []byte(body), 0o644)
}

// source returns a builder for a fresh provider per run and a label.
func source() (func(run int, retries *int) llm.Provider, string) {
	key := os.Getenv("GROQ_API_KEY")
	if key == "" {
		fmt.Fprintln(os.Stderr, "GROQ_API_KEY not set: running scripted mocks. This only proves the harness works, it measures no model.")
		return mockFor, "scripted mock"
	}
	model := os.Getenv("GROQ_MODEL")
	if model == "" {
		fmt.Fprintln(os.Stderr, "GROQ_MODEL is required when GROQ_API_KEY is set (see https://api.groq.com/openai/v1/models)")
		os.Exit(2)
	}
	return func(_ int, retries *int) llm.Provider {
		return &llm.Retrying{
			Inner: llm.NewGroq(key, model),
			OnRetry: func(attempt int, wait time.Duration, _ error) {
				*retries++
				fmt.Fprintf(os.Stderr, "  rate limited or unavailable, waiting %s (retry %d of 3)\n", wait, attempt)
			},
		}
	}, "groq:" + model
}

// mockFor plays a model for the keyless run: odd runs are clean, even runs
// break a few rules, so the report shows what a failure looks like.
func mockFor(run int, _ *int) llm.Provider {
	paras := make([]string, 12)
	for i := range paras {
		s := func(j int) string {
			return fmt.Sprintf("The slow river %d bends through the quiet meadow part %d tonight.", i, j)
		}
		paras[i] = strings.Join([]string{s(1), s(2), s(3), s(4)}, " ")
	}
	body := strings.Join(paras, "\n\n")
	if run%2 == 0 {
		body = strings.Replace(body, "tonight.", "tonight!", 1)
		body = strings.Replace(body, "quiet meadow", "meadow without any panic", 1)
	}
	return llm.NewMock(body, "Velvet Meadow")
}

func pause() time.Duration {
	if os.Getenv("GROQ_API_KEY") == "" {
		return 0
	}
	return time.Duration(envInt("SCRIPT_PAUSE_SECONDS", 15, 0, 600)) * time.Second
}

func envInt(name string, def, min, max int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < min || n > max {
		fmt.Fprintf(os.Stderr, "%s must be a number from %d to %d\n", name, min, max)
		os.Exit(2)
	}
	return n
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 200 {
		s = s[:200] + "..."
	}
	return s
}
