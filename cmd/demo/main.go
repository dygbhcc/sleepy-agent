// Command demo runs the Day 2 pipeline end to end with the mock provider.
// It needs no API key and no network.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"sleepy-agent/internal/llm"
	"sleepy-agent/internal/pipeline"
)

func main() {
	topic := flag.String("topic", "a quiet garden under the stars", "episode topic")
	flag.Parse()

	// Canned answers: the first is the title, the second is the script.
	provider := llm.NewMock(
		"The Garden Under the Stars",
		"Settle in. Let your shoulders soften. Somewhere beyond the hedge, the night air moves slowly through the leaves, "+
			"and every star above you is perfectly still.",
	)

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	ep, err := pipeline.Run(ctx, provider, *topic)
	if err != nil {
		fmt.Fprintln(os.Stderr, "run failed:", err)
		os.Exit(1)
	}

	fmt.Printf("provider : %s\n", provider.Name())
	fmt.Printf("topic    : %s\n", ep.Topic)
	fmt.Printf("title    : %s\n", ep.Title)
	fmt.Printf("script   : %s\n", ep.Script)
	fmt.Printf("tokens   : %d prompt, %d completion\n", ep.Usage.PromptTokens, ep.Usage.CompletionTokens)
}
