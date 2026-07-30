package main

import "testing"

func TestRun(t *testing.T) {
	if got := run(nil); got != 0 {
		t.Fatalf("run() = %d, want 0", got)
	}
}
