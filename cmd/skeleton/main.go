// Command skeleton runs the Day 4 walking skeleton: one episode goes from a
// topic to a published video through the whole state machine.
//
// Everything is a mock and the publisher is a dry run, so it needs no key and
// publishes nothing. What it shows is the shape: one step per iteration, and a
// publish step that waits for a human.
package main

import (
	"context"
	"fmt"
	"os"

	"sleepy-agent/internal/agent"
	"sleepy-agent/internal/llm"
	"sleepy-agent/internal/runner"
	"sleepy-agent/internal/store"
)

func main() {
	topic := "a quiet forest at night"
	if len(os.Args) > 1 {
		topic = os.Args[1]
	}
	if err := run(context.Background(), topic); err != nil {
		fmt.Fprintln(os.Stderr, "failed:", err)
		os.Exit(1)
	}
}

func run(ctx context.Context, topic string) error {
	st := store.NewMemory(nil)
	script := llm.NewMock("The Quiet Forest", "The forest is slow and warm. Breathe in. Breathe out.")

	w, err := runner.New(st, runner.Steps{
		Scripter:  runner.LLMScripter{Provider: script},
		Voice:     &runner.MockVoice{},
		Renderer:  &runner.MockRenderer{},
		Publisher: runner.DryRunPublisher{}, // the default: reports, never publishes
	}, runner.PublishGate{Runs: st}, runner.Config{WorkerID: "demo", Observer: printEvent})
	if err != nil {
		return err
	}

	r, err := st.CreateRun(ctx, topic)
	if err != nil {
		return err
	}
	fmt.Printf("topic     : %s\nrun       : %s\nadapters  : mock voice, mock renderer, dry-run publisher\n\n", topic, r.ID)

	fmt.Println("-- no approval given --")
	if err := w.Drain(ctx); err != nil {
		return err
	}
	got, _ := st.GetRun(ctx, r.ID)
	fmt.Printf("\nstatus: %s, waiting for approval: %v\n\n", got.Status, got.AwaitingApproval)

	fmt.Println("-- human approves publishing --")
	if err := st.ApprovePublish(ctx, r.ID); err != nil {
		return err
	}
	if err := w.Drain(ctx); err != nil {
		return err
	}
	got, _ = st.GetRun(ctx, r.ID)
	fmt.Printf("\nstatus: %s\npublished: %s\n", got.Status, got.Outputs["published"])
	return nil
}

func printEvent(e runner.Event) {
	switch {
	case e.Err != nil:
		fmt.Printf("  %-9s %s -> %s  ERROR: %v (%s)\n", e.Stage, e.From, e.To, e.Err, e.Reason)
	case e.Decision == agent.Ask:
		fmt.Printf("  %-9s %s  ASK: %s\n", e.Stage, e.From, e.Reason)
	case e.Decision == agent.Stop:
		fmt.Printf("  %-9s %s -> %s  STOP: %s\n", e.Stage, e.From, e.To, e.Reason)
	default:
		fmt.Printf("  %-9s %s -> %s\n", e.Stage, e.From, e.To)
	}
}
