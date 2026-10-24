package main

import (
	"path/filepath"
	"testing"
)

func TestMockOutputGoesToItsOwnFolder(t *testing.T) {
	if got, want := outDir(mockLabel), filepath.Join("out", "fixstats-mock"); got != want {
		t.Fatalf("mock dir = %q, want %q", got, want)
	}
	if got, want := outDir("groq:some-model"), filepath.Join("out", "fixstats"); got != want {
		t.Fatalf("real dir = %q, want %q", got, want)
	}
}
