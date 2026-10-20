package agent

import (
	"fmt"
	"testing"
	"time"
)

func TestLogRingKeepsTheLastLinesWhole(t *testing.T) {
	r := newLogRing(3)
	r.now = func() time.Time { return time.Date(2026, 9, 26, 13, 0, 0, 0, time.UTC) }

	fmt.Fprint(r, "one\ntw")
	fmt.Fprint(r, "o\r\nthree\nfour\n")
	fmt.Fprint(r, "unfinished")

	got := r.Last(0)
	want := []string{"13:00:00  two", "13:00:00  three", "13:00:00  four"}
	if len(got) != len(want) {
		t.Fatalf("got %q", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("line %d = %q, want %q", i, got[i], want[i])
		}
	}
	if last := r.Last(1); len(last) != 1 || last[0] != want[2] {
		t.Errorf("Last(1) = %q", last)
	}
}
