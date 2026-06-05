package logbuf

import (
	"fmt"
	"testing"
)

func TestHubSplitsAndSequences(t *testing.T) {
	h := New(100)
	fmt.Fprint(h, "first line\nsecond line\n")
	fmt.Fprint(h, "third ") // partial
	fmt.Fprint(h, "line\n") // completes the third

	all, last := h.Since(0)
	if len(all) != 3 || last != 3 {
		t.Fatalf("got %d entries (last=%d), want 3", len(all), last)
	}
	if all[0].Msg != "first line" || all[2].Msg != "third line" {
		t.Fatalf("unexpected lines: %q ... %q", all[0].Msg, all[2].Msg)
	}

	// Incremental poll returns only newer lines.
	fmt.Fprint(h, "fourth\n")
	nw, last := h.Since(3)
	if len(nw) != 1 || nw[0].Msg != "fourth" || last != 4 {
		t.Fatalf("incremental poll = %+v (last=%d)", nw, last)
	}
}

func TestHubRingCap(t *testing.T) {
	h := New(3)
	for i := range 10 {
		fmt.Fprintf(h, "line %d\n", i)
	}
	all, last := h.Since(0)
	if len(all) != 3 || last != 10 {
		t.Fatalf("cap not enforced: %d entries, last=%d", len(all), last)
	}
	// Oldest kept is line 7 (10 written, cap 3).
	if all[0].Msg != "line 7" {
		t.Fatalf("oldest kept = %q, want 'line 7'", all[0].Msg)
	}
}
