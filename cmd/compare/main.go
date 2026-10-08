// Command compare answers one question: for the same model and the same task,
// does native tool calling or prompt based tool calling work better?
//
// It runs the Day 3 task COMPARE_RUNS times (default 5) in each mode and
// prints one line per run plus a table you can paste into a post. Nothing is
// estimated: every number comes from a run that happened.
//
// Without GROQ_API_KEY it runs the scripted mocks, which only proves the
// harness works. With a key, GROQ_MODEL is required (no default model).
package main

import (
	"context"
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"sleepy-agent/internal/agent"
	"sleepy-agent/internal/errs"
	"sleepy-agent/internal/llm"
	"sleepy-agent/internal/scenario"
	"sleepy-agent/internal/tools"
)

type stats struct {
	mode      string
	runs      int
	completed int
	apiErrors int
	other     int // finished without the tools, or hit the step limit
	invalid   int // unusable replies, summed over runs
	retries   int // transient errors (rate limits) that were waited out and retried
	toolCalls int
	prompt    int
	complet   int
	firstErr  string
}

func main() {
	runs := 5
	if v := os.Getenv("COMPARE_RUNS"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 50 {
			fmt.Fprintln(os.Stderr, "COMPARE_RUNS must be a number from 1 to 50")
			os.Exit(2)
		}
		runs = n
	}

	label, build := source()
	fmt.Printf("pause between runs: %s, retries per model call on rate limit: %d\n", pause(), maxRetries)
	fmt.Printf("model: %s, %d runs per mode\n\n", label, runs)

	var all []stats
	for _, mode := range []string{scenario.ModePrompt, scenario.ModeNative} {
		st := stats{mode: mode, runs: runs}
		for i := 1; i <= runs; i++ {
			if i > 1 || mode != scenario.ModePrompt {
				time.Sleep(pause())
			}
			p, err := build(mode, &st.retries)
			if err != nil {
				fmt.Fprintln(os.Stderr, err)
				os.Exit(2)
			}
			res, err := runOnce(p)
			line := record(&st, res, err)
			fmt.Printf("%-7s run %d: %s\n", mode, i, line)
		}
		all = append(all, st)
		fmt.Println()
	}
	printTable(all)
}

func runOnce(p llm.Provider) (*agent.Result, error) {
	a, err := agent.New(p, tools.All(), agent.AlwaysAct{}, agent.Config{MaxSteps: 8})
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Minute)
	defer cancel()
	return a.Run(ctx, scenario.Task)
}

func record(st *stats, res *agent.Result, err error) string {
	if res != nil {
		st.invalid += scenario.InvalidReplies(res)
		st.toolCalls += res.ToolCalls
		st.prompt += res.Usage.PromptTokens
		st.complet += res.Usage.CompletionTokens
	}
	switch {
	case err != nil:
		st.apiErrors++
		msg := oneLine(err.Error())
		if st.firstErr == "" {
			st.firstErr = msg
		}
		return "ERROR " + msg
	case scenario.Completed(res):
		st.completed++
		return fmt.Sprintf("completed, %d tool calls, %d invalid replies", res.ToolCalls, scenario.InvalidReplies(res))
	default:
		st.other++
		return fmt.Sprintf("not completed (outcome %s, %d tool calls)", res.Outcome, res.ToolCalls)
	}
}

func printTable(all []stats) {
	fmt.Println("| mode | completed | API errors | not completed | invalid replies | rate-limit retries | avg tool calls | avg tokens (prompt+completion) |")
	fmt.Println("|---|---|---|---|---|---|---|---|")
	for _, s := range all {
		fmt.Printf("| %s | %d/%d | %d | %d | %d | %d | %.1f | %d |\n",
			s.mode, s.completed, s.runs, s.apiErrors, s.other, s.invalid, s.retries,
			float64(s.toolCalls)/float64(s.runs), (s.prompt+s.complet)/s.runs)
	}
	for _, s := range all {
		if s.firstErr != "" {
			fmt.Printf("\nfirst %s error: %s\n", s.mode, s.firstErr)
		}
	}
}

// source returns a label and a builder that makes a fresh provider per run
// (scripted mocks are single use).
func source() (string, func(mode string, retries *int) (llm.Provider, error)) {
	key := os.Getenv("GROQ_API_KEY")
	if key == "" {
		fmt.Fprintln(os.Stderr, "GROQ_API_KEY not set: running scripted mocks. This only proves the harness works, it measures no model.")
		return "scripted mock", func(mode string, _ *int) (llm.Provider, error) {
			if mode == scenario.ModeNative {
				return scenario.ScriptedNative(), nil
			}
			return llm.NewPromptTools(scenario.ScriptedPrompt()), nil
		}
	}
	model := os.Getenv("GROQ_MODEL")
	if model == "" {
		fmt.Fprintln(os.Stderr, "GROQ_MODEL is required when GROQ_API_KEY is set (see https://api.groq.com/openai/v1/models)")
		os.Exit(2)
	}
	return "groq:" + model, func(mode string, retries *int) (llm.Provider, error) {
		return scenario.Adapt(mode, &retrying{inner: llm.NewGroq(key, model), count: retries})
	}
}

func oneLine(s string) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > 160 {
		s = s[:160] + "..."
	}
	return s
}

const maxRetries = 3

// backoff is a variable so tests do not sleep for real.
var backoff = func(attempt int) time.Duration { return time.Duration(20*(attempt+1)) * time.Second }

// pause is the wait between runs, so a burst of runs does not trip the rate
// limit by itself. COMPARE_PAUSE_SECONDS overrides it, 0 disables it.
func pause() time.Duration {
	if _, ok := os.LookupEnv("GROQ_API_KEY"); !ok {
		return 0
	}
	if v := os.Getenv("COMPARE_PAUSE_SECONDS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			return time.Duration(n) * time.Second
		}
	}
	return 15 * time.Second
}

// retrying waits out transient errors (a 429 rate limit is not a model
// failure) and counts every retry, so the table says how often it happened.
// A provider that stays unavailable still ends as an API error.
type retrying struct {
	inner llm.Provider
	count *int
}

func (r *retrying) Name() string { return r.inner.Name() }

func (r *retrying) Complete(ctx context.Context, req llm.Request) (llm.Response, error) {
	for attempt := 0; ; attempt++ {
		resp, err := r.inner.Complete(ctx, req)
		if err == nil || !errs.IsTransient(err) || attempt >= maxRetries {
			return resp, err
		}
		*r.count++
		wait := backoff(attempt)
		fmt.Fprintf(os.Stderr, "  rate limited or unavailable, waiting %s (retry %d of %d)\n", wait, attempt+1, maxRetries)
		select {
		case <-time.After(wait):
		case <-ctx.Done():
			return llm.Response{}, ctx.Err()
		}
	}
}
