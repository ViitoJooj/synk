package service

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"strings"
	"time"
	"unicode/utf16"
)

// DecodeLog turns a log file into text whatever wrote it.
//
// On Windows the agent's output reaches its file through PowerShell's `*>`,
// and Windows PowerShell 5.1 writes that as UTF-16LE with a byte-order mark.
// Read as UTF-8 it comes out as letters separated by NUL bytes, which is
// unreadable in a terminal and matches nothing in a search.
func DecodeLog(raw []byte) string {
	switch {
	case len(raw) >= 2 && raw[0] == 0xFF && raw[1] == 0xFE:
		raw = raw[2:]
		units := make([]uint16, len(raw)/2)
		for i := range units {
			units[i] = uint16(raw[2*i]) | uint16(raw[2*i+1])<<8
		}
		return strings.ReplaceAll(string(utf16.Decode(units)), "\r\n", "\n")
	case len(raw) >= 3 && raw[0] == 0xEF && raw[1] == 0xBB && raw[2] == 0xBF:
		raw = raw[3:]
	}
	return strings.ReplaceAll(string(raw), "\r\n", "\n")
}

// LastLines is the tail of text, like tail -n.
func LastLines(text string, n int) string {
	text = strings.TrimRight(text, "\n")
	if text == "" || n <= 0 {
		return ""
	}
	lines := strings.Split(text, "\n")
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return strings.Join(lines, "\n") + "\n"
}

// FollowLog prints the last lines of the file at path and then, with follow,
// whatever is appended, until ctx is cancelled.
//
// The file is re-read whole on each poll rather than from an offset: a
// UTF-16 file cannot be decoded from an arbitrary byte, and these logs are
// small. A file that shrinks was rewritten by a restart, and is printed from
// its start again.
func FollowLog(ctx context.Context, w io.Writer, path string, lines int, follow bool) error {
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("no log at %s; the agent writes one only when installed with install.ps1 -Hidden", path)
		}
		return err
	}
	text := DecodeLog(raw)
	fmt.Fprint(w, LastLines(text, lines))
	if !follow {
		return nil
	}

	shown := len(text)
	tick := time.NewTicker(500 * time.Millisecond)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-tick.C:
		}

		raw, err := os.ReadFile(path)
		if err != nil {
			continue // mid-rewrite; the next poll sees the new file
		}
		text := DecodeLog(raw)
		if len(text) < shown {
			shown = 0
		}
		fmt.Fprint(w, text[shown:])
		shown = len(text)
	}
}
