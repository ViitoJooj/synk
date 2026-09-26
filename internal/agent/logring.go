package agent

import (
	"bytes"
	"sync"
	"time"
)

// logRing keeps the agent's last lines of output, for the "logs" verb: the UI
// reads them the same way on both systems, instead of from the journal on
// one and a UTF-16 file on the other.
type logRing struct {
	mu      sync.Mutex
	lines   []string
	max     int
	partial []byte
	now     func() time.Time
}

func newLogRing(max int) *logRing { return &logRing{max: max, now: time.Now} }

// Write takes output as it comes, which may split a line across calls.
func (r *logRing) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.partial = append(r.partial, p...)
	for {
		i := bytes.IndexByte(r.partial, '\n')
		if i < 0 {
			break
		}
		line := string(bytes.TrimRight(r.partial[:i], "\r"))
		r.partial = r.partial[i+1:]
		r.lines = append(r.lines, r.now().Format("15:04:05")+"  "+line)
		if len(r.lines) > r.max {
			r.lines = r.lines[len(r.lines)-r.max:]
		}
	}
	return len(p), nil
}

// Last returns up to n of the most recent lines, oldest first.
func (r *logRing) Last(n int) []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	if n <= 0 || n > len(r.lines) {
		n = len(r.lines)
	}
	return append([]string(nil), r.lines[len(r.lines)-n:]...)
}
