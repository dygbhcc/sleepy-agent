// Package script writes sleep narration scripts with a language model and
// measures them.
//
// Ported from the Sleepy production generator (internal/providers/llm): the
// chunked generation, the prompts, the style guides and the imagery sets are
// the same. What is not ported, on purpose:
//
//   - Banned word replacement after generation. It hides failures, and Day 5
//     exists to measure the raw ones. Day 6 decides what to do about them.
//   - Continuation mode. It belongs to the fix engine (Day 7).
//   - SSML. It belongs to the voice step (Day 10).
//   - frequency and presence penalty. Not every provider accepts them and
//     compatibility is not verified here.
package script

import (
	"bytes"
	"text/template"
)

// BannedPhrases are the words the system prompt forbids. Measure checks the
// output against exactly this list.
var BannedPhrases = []string{
	"suddenly", "blood", "scream", "terror", "panic", "kill", "dead", "gun", "fight", "attack",
	"explosion", "horror", "nightmare", "violent", "murder", "death", "war", "battle", "weapon",
	"destroy", "rage", "wound", "shriek", "jolt", "alarm", "collapse", "danger", "fear", "crash",
	"void", "disappear", "fade away", "swallow", "consume", "annihilate", "wake up",
	"open your eyes", "become alert",
}

const systemPrompt = `You are a sleep narration scriptwriter. Produce calm, slow-paced plain text for sleep videos.

ABSOLUTE RULES:
1. Never use any banned words: suddenly, blood, scream, terror, panic, kill, dead, gun, fight, attack, explosion, horror, nightmare, violent, murder, death, war, battle, weapon, destroy, rage, wound, shriek, jolt, alarm, collapse, danger, fear, crash, void, disappear, fade away, swallow, consume, annihilate, wake up, open your eyes, become alert.
2. Zero exclamation marks. Use periods, commas, and occasional semicolons only.
3. Every sentence must be 8-20 words. Every sentence ends with a period.
4. No ALL CAPS words.
5. No titles, headings, markdown, bullet points, numbered lists, SSML, or XML.
6. No ellipsis, em-dashes, or decorative punctuation.
7. No existential questions, philosophical musings, tension, conflict, or suspense.
8. No wake-up cues or references to waking, alertness, or opening eyes.
9. Plain prose paragraphs separated by blank lines. Nothing else.
10. No inspirational tone. No motivational language. No transformation narrative. No destiny or purpose language

SELF-CHECK:
Before you answer, scan for banned words and sentence length violations.
Fix silently. Return only the script.`

var langNames = map[string]string{
	"en": "English",
	"tr": "Turkish",
	"pt": "Portuguese",
	"es": "Spanish",
	"it": "Italian",
}

var styleGuides = map[string]string{
	"Cosmos": "Imagery of deep space, drifting nebulae, distant stars, gentle cosmic winds, " +
		"the silence between galaxies, soft light from faraway suns, slowly turning planets.",
	"Earthside": "Imagery of quiet forests, still lakes, gentle rainfall, moss-covered stones, " +
		"slow rivers, meadows at dusk, soft moonlight on rolling hills.",
	"Myth": "Imagery of enchanted gardens, ancient sleeping libraries, gentle mythical creatures at rest, " +
		"soft lantern light, timeless quiet villages, dreaming mountains.",
}

var imagerySets = map[string][]string{
	"Cosmos": {
		"Soft starlight on distant dust.",
		"Slow moons in wide orbits.",
		"Nebula haze with muted colors.",
		"Quiet planet nightsides and thin atmospheres.",
		"Gentle rings and faint ice grains.",
		"Far suns as warm, steady glow.",
		"Constellation shapes and slow drifting perspective.",
		"Calm cosmic winds as soft movement of light.",
		"Deep space textures: velvet dark, soft gradients, distant shimmer.",
		"Very quiet closing stillness with dim, steady light.",
	},
	"Earthside": {
		"Dew forming on cool morning grass.",
		"Still lake reflecting pale sky.",
		"Moss growing on ancient stones.",
		"Slow river bending through quiet meadow.",
		"Soft rain on broad green leaves.",
		"Fireflies drifting in warm dusk air.",
		"Fog settling into low valleys.",
		"Moonlight pooling on forest floor.",
		"Birch bark peeling in gentle wind.",
		"Snow falling softly on sleeping fields.",
	},
	"Myth": {
		"Lantern glow in an old stone garden.",
		"Sleeping dragon curled near warm embers.",
		"Ancient library with dusty, dreaming books.",
		"Quiet village under a canopy of stars.",
		"Silver thread of a forgotten lullaby.",
		"Gentle giant resting in a flower meadow.",
		"Enchanted well reflecting a calm moon.",
		"Soft chimes in a temple courtyard.",
		"Woven tapestry showing peaceful legends.",
		"Mist over a bridge to quiet lands.",
	},
}

type chunkData struct {
	ParagraphCount   int
	IsFirstChunk     bool
	IsLastChunk      bool
	Series           string
	Episode          string
	LangName         string
	StyleDescription string
	ImagerySets      []string
	PreviousContext  string
	ChunkMinWords    int
	ChunkMaxWords    int
}

var chunkTmpl = template.Must(template.New("chunk").Funcs(template.FuncMap{
	"add": func(a, b int) int { return a + b },
}).Parse(`CONTENT GOAL:
Write exactly {{.ParagraphCount}} paragraphs.
Each paragraph must contain exactly 4 sentences.
Every sentence must be 8-20 words and end with a period.
This structure is mandatory.
{{if .IsFirstChunk}}
WORD COUNT GOAL:
The total word count MUST be {{.ChunkMinWords}}-{{.ChunkMaxWords}} words. If below {{.ChunkMinWords}}, continue writing. If above {{.ChunkMaxWords}}, stop immediately before exceeding {{.ChunkMaxWords}}
{{else}}
WORD COUNT GOAL:
Continue the script. Write {{.ChunkMinWords}}-{{.ChunkMaxWords}} new words.
Do NOT repeat any existing text.
{{end}}
STYLE:
Series: {{.Series}}
Episode: {{.Episode}}
Language: {{.LangName}}
Present tense only.
{{.StyleDescription}}

VARIETY WITHOUT REPETITION:
Use these imagery sets and rotate through them, never repeating a full sentence:
{{range $i, $img := .ImagerySets}}{{add $i 1}}. {{$img}}
{{end}}
PACING:
{{if .IsFirstChunk}}Begin extremely gentle.{{else}}Continue from where the previous section ended.{{end}}
Each paragraph becomes slightly softer and quieter.
{{if .IsLastChunk}}End with the softest imagery and stillness.{{end}}
{{if .PreviousContext}}
=== PREVIOUS CONTEXT (last 2 paragraphs, DO NOT REPEAT) ===
{{.PreviousContext}}
=== END CONTEXT ===
{{end}}
Output only the {{.ParagraphCount}} new paragraphs. Nothing else.`))

func buildChunkPrompt(d chunkData) (string, error) {
	var buf bytes.Buffer
	if err := chunkTmpl.Execute(&buf, d); err != nil {
		return "", err
	}
	return buf.String(), nil
}
