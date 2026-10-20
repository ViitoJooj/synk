package service

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
	"unicode/utf16"
)

// utf16le is what Windows PowerShell 5.1 writes for `*>`.
func utf16le(s string) []byte {
	out := []byte{0xFF, 0xFE}
	for _, u := range utf16.Encode([]rune(s)) {
		out = append(out, byte(u), byte(u>>8))
	}
	return out
}

func TestDecodeLogReadsPowerShellOutput(t *testing.T) {
	got := DecodeLog(utf16le("link down: peer offline\r\nconectado ao notebook\r\n"))
	if got != "link down: peer offline\nconectado ao notebook\n" {
		t.Errorf("got %q", got)
	}
}

func TestDecodeLogReadsUTF8(t *testing.T) {
	bom := append([]byte{0xEF, 0xBB, 0xBF}, "ação\n"...)
	if got := DecodeLog(bom); got != "ação\n" {
		t.Errorf("got %q", got)
	}
	if got := DecodeLog([]byte("plain\n")); got != "plain\n" {
		t.Errorf("got %q", got)
	}
}

func TestLastLines(t *testing.T) {
	if got := LastLines("a\nb\nc\n", 2); got != "b\nc\n" {
		t.Errorf("got %q", got)
	}
	if got := LastLines("a\n", 10); got != "a\n" {
		t.Errorf("got %q", got)
	}
	if got := LastLines("", 10); got != "" {
		t.Errorf("got %q", got)
	}
}

// syncBuffer is read by the test while FollowLog writes to it.
type syncBuffer struct {
	mu  chan struct{}
	buf bytes.Buffer
}

func newSyncBuffer() *syncBuffer { return &syncBuffer{mu: make(chan struct{}, 1)} }

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu <- struct{}{}
	defer func() { <-b.mu }()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu <- struct{}{}
	defer func() { <-b.mu }()
	return b.buf.String()
}

func TestFollowLogPrintsWhatIsAppended(t *testing.T) {
	path := filepath.Join(t.TempDir(), "synk.log")
	if err := os.WriteFile(path, utf16le("one\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	out := newSyncBuffer()
	done := make(chan error)
	go func() { done <- FollowLog(ctx, out, path, 10, true) }()

	if err := os.WriteFile(path, utf16le("one\r\ntwo\r\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(3 * time.Second)
	for !strings.Contains(out.String(), "two\n") && time.Now().Before(deadline) {
		time.Sleep(50 * time.Millisecond)
	}
	cancel()
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	if got := out.String(); got != "one\ntwo\n" {
		t.Errorf("got %q", got)
	}
}

func TestFollowLogExplainsAMissingFile(t *testing.T) {
	err := FollowLog(context.Background(), &bytes.Buffer{}, filepath.Join(t.TempDir(), "none.log"), 10, false)
	if err == nil || !strings.Contains(err.Error(), "-Hidden") {
		t.Errorf("err = %v", err)
	}
}
