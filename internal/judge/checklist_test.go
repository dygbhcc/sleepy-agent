package judge

import (
	"context"
	"errors"
	"strings"
	"testing"

	"sleepy-agent/internal/llm"
)

const clBody = "The lantern glows over the quiet harbor tonight.\n\nSnow settles on the roof. Fireflies drift above the warm grass.\n\nThe tide slows and the water rests."

func reviewCL(t *testing.T, c Checklist) ChecklistResult {
	t.Helper()
	res, err := c.Review(context.Background(), "a lighthouse", "Lighthouse", clBody)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestChecklistRejectsAWeatherConflictWithBothQuotes(t *testing.T) {
	m := llm.NewMock(`{"checks":[{"id":"H5a","present":true,"quotes":["Snow settles on the roof.","Fireflies drift above the warm grass."]}]}`)
	res := reviewCL(t, Checklist{Provider: m, NoConfirm: true})
	if len(res.Findings) != 1 || res.Findings[0].ID != "H5a" || !res.Findings[0].Reject {
		t.Fatalf("want one rejecting H5a, got %+v", res.Findings)
	}
}

func TestChecklistDropsQuotesThatAreNotInTheScript(t *testing.T) {
	m := llm.NewMock(`{"checks":[{"id":"H5a","present":true,"quotes":["Snow falls on a desert dune."]}]}`)
	res := reviewCL(t, Checklist{Provider: m, NoConfirm: true})
	if len(res.Findings) != 0 || len(res.Dropped) != 1 || res.Dropped[0] != "H5a" {
		t.Fatalf("invented quote must be dropped, got %+v dropped %v", res.Findings, res.Dropped)
	}
}

func TestChecklistOneConflictQuoteOnlyWarns(t *testing.T) {
	m := llm.NewMock(`{"checks":[{"id":"H5a","present":true,"quotes":["Snow settles on the roof."]}]}`)
	f := reviewCL(t, Checklist{Provider: m, NoConfirm: true}).Findings
	if len(f) != 1 || f[0].Reject || !f[0].Warn {
		t.Fatalf("one quote should warn only, got %+v", f)
	}
}

func TestChecklistConfirmPassKeepsOnlyQuotesBothPassesReturn(t *testing.T) {
	first := llm.Response{Content: `{"checks":[{"id":"H5a","present":true,"quotes":["Snow settles on the roof.","Fireflies drift above the warm grass."]}]}`}
	second := llm.Response{Content: `{"checks":[{"id":"H5a","present":true,"quotes":["snow settles on the roof."]}]}`}
	m := llm.NewMockResponses(first, second)
	f := reviewCL(t, Checklist{Provider: m}).Findings
	if len(f) != 1 || f[0].Reject || f[0].Count != 1 {
		t.Fatalf("intersection should leave one quote and a warning, got %+v", f)
	}
	if len(m.Requests()) != 2 {
		t.Fatalf("want 2 calls, got %d", len(m.Requests()))
	}
}

func TestChecklistConfirmPassIsSkippedWhenNothingIsFound(t *testing.T) {
	m := llm.NewMock(`{"checks":[{"id":"H5a","present":false}]}`)
	reviewCL(t, Checklist{Provider: m})
	if len(m.Requests()) != 1 {
		t.Fatalf("want 1 call, got %d", len(m.Requests()))
	}
}

func TestChecklistUnsureDependsOnMode(t *testing.T) {
	reply := `{"checks":[{"id":"H6","present":false,"unsure":true}]}`
	if f := reviewCL(t, Checklist{Provider: llm.NewMock(reply), Mode: ModeOwnerReviews}).Findings; len(f) != 0 {
		t.Fatalf("owner mode: unsure is not found, got %+v", f)
	}
	f := reviewCL(t, Checklist{Provider: llm.NewMock(reply), Mode: ModeAutonomous}).Findings
	if len(f) != 1 || !f[0].Unsure || f[0].Reject {
		t.Fatalf("autonomous mode: want an unsure finding, got %+v", f)
	}
}

func TestChecklistTruncationAndBadJSONAreErrors(t *testing.T) {
	trunc := llm.NewMockResponses(llm.Response{Content: `{"checks":[`, FinishReason: "length"})
	_, err := Checklist{Provider: trunc}.Review(context.Background(), "t", "t", clBody)
	if !errors.Is(err, ErrTruncated) {
		t.Fatalf("want ErrTruncated, got %v", err)
	}
	_, err = Checklist{Provider: llm.NewMock("no json here")}.Review(context.Background(), "t", "t", clBody)
	if !errors.Is(err, ErrBadVerdict) {
		t.Fatalf("want ErrBadVerdict, got %v", err)
	}
}

func TestChecklistShowsNumberedParagraphs(t *testing.T) {
	m := llm.NewMock(`{"checks":[]}`)
	reviewCL(t, Checklist{Provider: m})
	user := m.Requests()[0].Messages[1].Content
	if !strings.Contains(user, "[P1]") || !strings.Contains(user, "[P3]") {
		t.Fatalf("paragraph markers missing: %q", user)
	}
}

func TestChecklistH5bOnlyFinalSentenceWarns(t *testing.T) {
	m := llm.NewMock(`{"checks":[{"id":"H5b","present":true,"quotes":["The tide slows and the water rests."]}]}`)
	f := reviewCL(t, Checklist{Provider: m, NoConfirm: true}).Findings
	if len(f) != 1 || f[0].Reject {
		t.Fatalf("final sentence only should warn, got %+v", f)
	}
	m = llm.NewMock(`{"checks":[{"id":"H5b","present":true,"quotes":["Snow settles on the roof."]}]}`)
	f = reviewCL(t, Checklist{Provider: m, NoConfirm: true}).Findings
	if len(f) != 1 || !f[0].Reject {
		t.Fatalf("earlier sentence should reject, got %+v", f)
	}
}

func TestChecklistH10RejectsOnlyWhenMostParagraphsCount(t *testing.T) {
	q := `"quotes":["The lantern glows over the quiet harbor tonight."]`
	f := reviewCL(t, Checklist{Provider: llm.NewMock(`{"checks":[{"id":"H10","present":true,"count":2,` + q + `}]}`), NoConfirm: true}).Findings
	if len(f) != 1 || !f[0].Reject {
		t.Fatalf("2 of 3 paragraphs should reject, got %+v", f)
	}
	f = reviewCL(t, Checklist{Provider: llm.NewMock(`{"checks":[{"id":"H10","present":true,"count":1,` + q + `}]}`), NoConfirm: true}).Findings
	if len(f) != 1 || f[0].Reject {
		t.Fatalf("1 of 3 should warn, got %+v", f)
	}
}

func TestChecklistTraceShowsWhatTheModelAnswered(t *testing.T) {
	m := llm.NewMock(`{"checks":[{"id":"H4","present":true,"quotes":["not in the script at all"]},{"id":"H6","present":false,"unsure":true}]}`)
	res := reviewCL(t, Checklist{Provider: m, NoConfirm: true})
	joined := strings.Join(res.Trace, "|")
	if !strings.Contains(joined, "H4 present=true unsure=false quotes=1 valid=0") || !strings.Contains(joined, "H6 present=false unsure=true") || !strings.Contains(joined, "H2b not answered") {
		t.Fatalf("trace: %v", res.Trace)
	}
}

func TestChecklistTraceShowsWhyTheConfirmPassDroppedADefect(t *testing.T) {
	first := `{"checks":[{"id":"H5b","present":true,"quotes":["Snow settles on the roof."]}]}`
	second := `{"checks":[{"id":"H5b","present":false,"quotes":[]}]}`
	res := reviewCL(t, Checklist{Provider: llm.NewMock(first, second)})
	if len(res.Findings) != 0 || len(res.Dropped) != 1 || res.Dropped[0] != "H5b" {
		t.Fatalf("a defect the confirm pass denies must be dropped: %+v", res)
	}
	joined := strings.Join(res.Trace, "|")
	if !strings.Contains(joined, "H5b present=true unsure=false quotes=0 valid=0 first=1 confirm_present=false confirm_quotes=0") {
		t.Fatalf("trace should say the quote existed in the first pass only: %v", res.Trace)
	}
}

func TestChecklistConfirmByPresenceKeepsADefectWhenThePassesQuoteDifferentSentences(t *testing.T) {
	first := `{"checks":[{"id":"H5b","present":true,"quotes":["Snow settles on the roof."]}]}`
	second := `{"checks":[{"id":"H5b","present":true,"quotes":["The tide slows and the water rests."]}]}`
	byQuote := reviewCL(t, Checklist{Provider: llm.NewMock(first, second)})
	if len(byQuote.Findings) != 0 {
		t.Fatalf("default: different quotes must drop the defect, got %+v", byQuote.Findings)
	}
	byPresence := reviewCL(t, Checklist{Provider: llm.NewMock(first, second), ConfirmByPresence: true})
	if len(byPresence.Findings) != 1 || byPresence.Findings[0].ID != "H5b" {
		t.Fatalf("by presence: want H5b kept, got %+v", byPresence)
	}
	denied := `{"checks":[{"id":"H5b","present":false,"quotes":[]}]}`
	gone := reviewCL(t, Checklist{Provider: llm.NewMock(first, denied), ConfirmByPresence: true})
	if len(gone.Findings) != 0 {
		t.Fatalf("by presence: a denied defect must still be dropped, got %+v", gone.Findings)
	}
}
