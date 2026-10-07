package policy

import "testing"

func TestForDuration(t *testing.T) {
	got := ForDuration(5)
	want := Policy{MinWords: 500, MaxWords: 800, TargetWords: 650}
	if got != want {
		t.Fatalf("ForDuration(5) = %+v, want %+v", got, want)
	}
}
