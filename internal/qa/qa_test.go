package qa

import (
	"fmt"
	"strings"
	"testing"

	"sleepy-agent/internal/policy"
	"sleepy-agent/internal/script"
)

var five = policy.ForDuration(5) // 500 to 800 words

const goodTitle = "A Quiet Forest"

func good() string { return script.SampleScript(12) }

func TestAGoodScriptPasses(t *testing.T) {
	r := Script(goodTitle, good(), five)
	if !r.Pass || r.FailType != "" {
		t.Fatalf("want pass, got %s", r.Summary())
	}
	if r.Summary() != "passed" {
		t.Fatalf("summary = %q", r.Summary())
	}
}

// These two were the Day 2 known gap tests: the naive pipeline accepted both.
// They now expect a rejection.

func TestGateRejectsATooShortScript(t *testing.T) {
	r := Script(goodTitle, "Close your eyes. Breathe slowly.", five)
	if r.Pass || r.FailType != FailWordcountLow {
		t.Fatalf("want WORDCOUNT_LOW, got pass=%v %s", r.Pass, r.Summary())
	}
	if !strings.Contains(r.Summary(), "word count 5 < minimum 500") {
		t.Fatalf("the reason must carry the numbers: %s", r.Summary())
	}
}

func TestGateRejectsOneSentenceRepeatedSixtyTimes(t *testing.T) {
	body := strings.TrimSpace(strings.Repeat("The night is calm and the stars drift slowly by. ", 60))
	r := Script(goodTitle, body, five)
	// 600 words, so the length check passes. Only the repetition check can see it.
	if r.Pass || r.FailType != FailPacing {
		t.Fatalf("want PACING_FAIL, got pass=%v %s", r.Pass, r.Summary())
	}
	if !strings.Contains(r.Summary(), "high repetition") {
		t.Fatalf("summary = %s", r.Summary())
	}
}

func TestGateRejectsATooLongScript(t *testing.T) {
	r := Script(goodTitle, script.SampleScript(14), five) // 728 words
	if !r.Pass {
		t.Fatalf("setup: 728 words must still pass, got %s", r.Summary())
	}
	r = Script(goodTitle, script.SampleScript(16), five) // 832 words
	if r.Pass || r.FailType != FailWordcountHigh {
		t.Fatalf("want WORDCOUNT_HIGH, got %s", r.Summary())
	}
}

func TestWordCountLimitsAreInclusive(t *testing.T) {
	exact := func(n int) string { return strings.TrimSpace(strings.Repeat("w ", n)) }
	// plain words are fine for the length check; look only at that check
	for _, tc := range []struct {
		words int
		want  bool
	}{{499, false}, {500, true}, {800, true}, {801, false}} {
		var wc Check
		for _, c := range Script(goodTitle, exact(tc.words), five).Checks {
			if c.Name == "wordcount" {
				wc = c
			}
		}
		if wc.Pass != tc.want {
			t.Errorf("%d words: wordcount pass = %v, want %v (%s)", tc.words, wc.Pass, tc.want, wc.Details)
		}
	}
}

func TestBannedPhraseFailsWithTheWordAndCount(t *testing.T) {
	body := good() + "\n\nThe war came and the war left."
	r := Script(goodTitle, body, five)
	if r.Pass || r.FailType != FailBannedPhrase {
		t.Fatalf("want BANNED_PHRASE, got %s", r.Summary())
	}
	if !strings.Contains(r.Summary(), `"war" (x2)`) {
		t.Fatalf("summary = %s", r.Summary())
	}
}

func TestBannedWordsMatchWholeWordsOnly(t *testing.T) {
	body := good() + "\n\nA warm feather rests on the warden's glove."
	if r := Script(goodTitle, body, five); !r.Pass {
		t.Fatalf("warm, feather and warden are not banned words: %s", r.Summary())
	}
}

func TestOnlyTheFirstThreeBannedPhrasesAreReported(t *testing.T) {
	body := good() + "\n\nSuddenly blood and terror and panic and war."
	n := 0
	for _, c := range Script(goodTitle, body, five).Checks {
		if c.Name == "banned_phrase" {
			n++
		}
	}
	if n != 3 {
		t.Fatalf("banned_phrase checks = %d, want 3", n)
	}
}

func TestSleepSafetyThresholdsAreSleepysNotThePromptsStrictOnes(t *testing.T) {
	// Two exclamation marks and three ALL CAPS words are tolerated by the gate,
	// although script.Measure counts them as violations of the prompt.
	body := good() + "\n\nThe ALPS and the NILE and the ANDES rest. Oh! Ah!"
	if r := Script(goodTitle, body, five); !r.Pass {
		t.Fatalf("at the threshold the gate passes: %s", r.Summary())
	}
	if rep := script.Measure(body, five); rep.Clean() {
		t.Fatal("setup: Measure must still count these as violations")
	}

	r := Script(goodTitle, body+" Wow!", five)
	if r.Pass || r.FailType != FailPacing || !strings.Contains(r.Summary(), "too many exclamation marks: 3 (max 2)") {
		t.Fatalf("three exclamation marks must fail: %s", r.Summary())
	}
	r = Script(goodTitle, good()+"\n\nThe ALPS and the NILE and the ANDES and the URAL rest.", five)
	if r.Pass || !strings.Contains(r.Summary(), "too many ALL CAPS words: 4 (max 3)") {
		t.Fatalf("four caps words must fail: %s", r.Summary())
	}
}

func TestAverageSentenceLengthOverTwentyFiveWordsFails(t *testing.T) {
	// 25 sentences of 26 words, 650 words in all, with enough different words
	// that the repetition check stays quiet.
	var b strings.Builder
	for i := 0; i < 25; i++ {
		fmt.Fprintf(&b, "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau upsilon phi chi s%da s%db s%dc s%dd. ", i, i, i, i)
	}
	r := Script(goodTitle, b.String(), five)
	if r.Pass || r.FailType != FailPacing || r.Summary() != "PACING_FAIL: average sentence too long: 26.0 words (max 25)" {
		t.Fatalf("got pass=%v %s", r.Pass, r.Summary())
	}
	// 25 words per sentence is the limit and still passes.
	b.Reset()
	for i := 0; i < 26; i++ {
		fmt.Fprintf(&b, "alpha beta gamma delta epsilon zeta eta theta iota kappa lambda mu nu xi omicron pi rho sigma tau upsilon phi s%da s%db s%dc s%dd. ", i, i, i, i)
	}
	if r := Script(goodTitle, b.String(), five); !r.Pass {
		t.Fatalf("25 word sentences are at the limit: %s", r.Summary())
	}
}

func TestShortSentencesAreNotAGateFailure(t *testing.T) {
	// The measured Qwen failure: valid structure, 5 to 7 word sentences. The
	// gate does not look at sentence length at all. Only the resulting word
	// count can stop it, and here it is long enough.
	var b strings.Builder
	for i := 0; i < 130; i++ {
		b.WriteString("Soft rain" + strings.Repeat(" ", 1) + "falls on stone" + string(rune('a'+i%26)) + string(rune('a'+(i/26)%26)) + " tonight. ")
	}
	r := Script(goodTitle, b.String(), five)
	if !r.Pass {
		t.Fatalf("short sentences alone must not fail the gate: %s", r.Summary())
	}
}

func TestTitleRules(t *testing.T) {
	long := strings.Repeat("a", 41)
	for _, tc := range []struct {
		title, want string
	}{
		{"", "title is empty"},
		{"   ", "title is empty"},
		{"Moon", "title too short (4 chars, min 5)"},
		{long, "title too long (41 chars, max 40)"},
		{"THE QUIET FOREST", "title is ALL CAPS"},
		{"The Fear of Night", `title contains banned phrase: "fear"`},
	} {
		r := Script(tc.title, good(), five)
		if r.Pass || r.FailType != FailTitleInvalid || !strings.Contains(r.Summary(), tc.want) {
			t.Errorf("title %q: want TITLE_INVALID with %q, got pass=%v %s", tc.title, tc.want, r.Pass, r.Summary())
		}
	}
	for _, ok := range []string{"Warm Meadow", "Moss and Silver Mist", strings.Repeat("a", 40), "Velvet", "Aurora", "Lunar"} {
		if r := Script(ok, good(), five); !r.Pass {
			t.Errorf("title %q should pass: %s", ok, r.Summary())
		}
	}
}

func TestTheFirstFailureDecidesTheFailType(t *testing.T) {
	// too short AND a bad title: the length reason wins, both are listed.
	r := Script("", "Close your eyes.", five)
	if r.FailType != FailWordcountLow {
		t.Fatalf("FailType = %s", r.FailType)
	}
	if !strings.Contains(r.Summary(), "(and 1 more)") {
		t.Fatalf("summary should count the other failure: %s", r.Summary())
	}
}

func TestEmptyBodyFailsLength(t *testing.T) {
	if r := Script(goodTitle, "", five); r.Pass || r.FailType != FailWordcountLow {
		t.Fatalf("got %s", r.Summary())
	}
}

func TestReportKeepsTheNumbersBehindTheVerdict(t *testing.T) {
	r := Script(goodTitle, "Close your eyes.", five)
	d := r.Checks[0].Diag
	if d["actual_words"] != 3 || d["min_words"] != 500 || d["target_words"] != 650 || d["delta_words"] != 3-650 {
		t.Fatalf("diag = %v", d)
	}
}

func TestShortAllCapsWordsAreIgnored(t *testing.T) {
	// "OK" and "TV" are two letters, Sleepy does not count them as shouting.
	body := good() + "\n\nOK TV OK TV OK TV OK TV rest here."
	if r := Script(goodTitle, body, five); !r.Pass {
		t.Fatalf("two letter caps words must not count: %s", r.Summary())
	}
}
