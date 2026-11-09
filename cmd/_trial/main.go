// Listening trial, not part of the pipeline: one script with long, flowing
// sentences, written to out/trial-long/script.txt for a listening test.
// It changes no repo prompt and measures nothing.
package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"sleepy-agent/internal/llm"
)

const system = `You are a sleep narration scriptwriter. Produce calm, slow-paced plain text that will be listened to, like a lullaby read aloud.

RULES:
1. Never use these words: suddenly, blood, scream, terror, panic, kill, dead, gun, fight, attack, explosion, horror, nightmare, violent, murder, death, war, battle, weapon, destroy, rage, wound, shriek, jolt, alarm, collapse, danger, fear, crash, void, disappear, fade away, swallow, consume, annihilate, wake up, open your eyes, become alert.
2. No exclamation marks, no ellipsis, no em dashes, no titles, no markdown. Plain prose paragraphs separated by blank lines.
3. No tension, no questions, no predators, no sudden events, no insects.
4. Present tense only. Every image comes from the world of the topic and none from outside it. Never repeat an image or a full sentence.

RHYTHM, this matters most:
5. Write 8 paragraphs of exactly 3 sentences. Every sentence is 22 to 35 words long, ends with a period, and reads like one slow breath.
6. Never write a short sentence. Vary the length a little, between 22 and 35 words.
7. Build sentences in different ways: join the parts with "as", "while", "where", "beneath", "with", a participle phrase, or a comma. The word "and" may appear at most once in a sentence, and never as a repeated pattern like "X, and Y, and Z".
8. Every sentence ends on a soft, rounded, two or three syllable word with an open sound (for example "slowly", "softly", "stillness", "rest", "shadow", "water"). Never end on a short, sharp or hard word. Do not end two sentences in a row on the same word, and use no ending word more than twice in the whole script.
9. Each paragraph is slightly softer and quieter than the one before. End with the softest imagery.
Total length at least 600 words, 8 paragraphs times 3 sentences times about 25 words. Return only the script.`

func main() {
	key, model := os.Getenv("GROQ_API_KEY"), os.Getenv("GROQ_MODEL")
	if key == "" || model == "" {
		fmt.Fprintln(os.Stderr, "set GROQ_API_KEY and GROQ_MODEL")
		os.Exit(2)
	}
	topic := os.Getenv("TOPIC")
	if topic == "" {
		topic = "an old lighthouse on a quiet coast at night"
	}
	g := llm.NewGroq(key, model)
	if err := g.SetReasoningEffort(os.Getenv("REASONING_EFFORT")); err != nil {
		fmt.Fprintln(os.Stderr, "REASONING_EFFORT:", err)
		os.Exit(2)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
	defer cancel()
	resp, err := g.Complete(ctx, llm.Request{
		Messages: []llm.Message{
			{Role: llm.RoleSystem, Content: system},
			{Role: llm.RoleUser, Content: "Topic: " + topic},
		},
		Temperature: 0.8,
		MaxTokens:   6000,
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
	if resp.FinishReason == "length" {
		fmt.Fprintln(os.Stderr, "truncated (finish_reason length): raise MaxTokens or lower REASONING_EFFORT")
		os.Exit(1)
	}
	body := strings.TrimSpace(resp.Content)
	_ = os.MkdirAll("out/trial-long", 0o755)
	if err := os.WriteFile("out/trial-long/script.txt", []byte(body+"\n"), 0o644); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	fmt.Println(body)
	fmt.Fprintf(os.Stderr, "\nwords: %d, saved to out/trial-long/script.txt\n", len(strings.Fields(body)))
}
