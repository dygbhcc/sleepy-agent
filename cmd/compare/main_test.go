package main

import (
	"errors"
	"testing"

	"sleepy-agent/internal/agent"
)

func TestRecordSeparatesErrorsFromFailures(t *testing.T) {
	st := stats{}
	record(&st, nil, errors.New("HTTP 400"))
	record(&st, &agent.Result{Outcome: agent.StepLimit}, nil)
	if st.apiErrors != 1 || st.other != 1 || st.completed != 0 {
		t.Fatalf("unexpected stats %+v", st)
	}
	if st.firstErr != "HTTP 400" {
		t.Fatalf("first error not kept: %q", st.firstErr)
	}
}
