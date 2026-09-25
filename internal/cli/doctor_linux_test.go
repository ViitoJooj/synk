//go:build linux

package cli

import (
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ViitoJooj/synk/internal/config"
)

func cfgWithSocket(path string) *config.Config {
	return &config.Config{Socket: path}
}

func onlyCheck(t *testing.T, checks []check) check {
	t.Helper()
	if len(checks) != 1 {
		t.Fatalf("got %d checks, want 1", len(checks))
	}
	return checks[0]
}

func TestDaemonCheckPassesWhenTheSocketAnswers(t *testing.T) {
	path := filepath.Join(t.TempDir(), "daemon.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer l.Close()

	got := onlyCheck(t, checkDaemon(cfgWithSocket(path)))

	if !got.ok {
		t.Errorf("ok = false (%s), want true", got.detail)
	}
}

func TestDaemonCheckReportsAMissingSocketAsNotRunning(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent.sock")

	got := onlyCheck(t, checkDaemon(cfgWithSocket(path)))

	if got.ok {
		t.Fatal("ok = true, want false")
	}
	if !strings.Contains(got.fix, "synk up") {
		t.Errorf("fix = %q, want it to suggest starting the service", got.fix)
	}
}

// The case that actually happened: the daemon is running and healthy, but this
// login session predates the group that gates its socket. Telling the user to
// start an already-running service sends them the wrong way entirely.
func TestDaemonCheckDistinguishesPermissionDeniedFromNotRunning(t *testing.T) {
	dir := t.TempDir()
	locked := filepath.Join(dir, "locked")
	if err := os.Mkdir(locked, 0o700); err != nil {
		t.Fatalf("Mkdir: %v", err)
	}
	path := filepath.Join(locked, "daemon.sock")
	l, err := net.Listen("unix", path)
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	defer l.Close()

	// Take away our own access to the directory, which is what group
	// membership does on the real socket.
	if err := os.Chmod(locked, 0o000); err != nil {
		t.Fatalf("Chmod: %v", err)
	}
	t.Cleanup(func() { os.Chmod(locked, 0o700) })

	if os.Geteuid() == 0 {
		t.Skip("running as root defeats the permission check")
	}

	got := onlyCheck(t, checkDaemon(cfgWithSocket(path)))

	if got.ok {
		t.Fatal("ok = true, want false")
	}
	if strings.Contains(got.fix, "systemctl start") {
		t.Errorf("fix = %q, but the daemon is running — this sends the user the wrong way", got.fix)
	}
	if !strings.Contains(got.detail, "permission") && !strings.Contains(got.detail, "Permission") {
		t.Errorf("detail = %q, want it to name the permission problem", got.detail)
	}
	// The real fix is a fresh login session, so the advice has to say so.
	if !strings.Contains(got.fix, "log") && !strings.Contains(got.fix, "su -") {
		t.Errorf("fix = %q, want it to explain that group membership needs a new session", got.fix)
	}
}
