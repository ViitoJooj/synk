package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/ViitoJooj/synk/internal/proto"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "conn.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("writing config: %v", err)
	}
	return path
}

func TestLoadsThisMachinesRealConfig(t *testing.T) {
	path := writeConfig(t, `
peer: desktop-1
port: 27431
edge: left
audio:
  playback: true
  mic: true
clipboard: true
`)

	c, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if c.Peer != "desktop-1" {
		t.Errorf("Peer = %q, want desktop-1", c.Peer)
	}
	if c.Port != 27431 {
		t.Errorf("Port = %d, want 27431", c.Port)
	}
	if c.Edge != proto.EdgeLeft {
		t.Errorf("Edge = %v, want EdgeLeft", c.Edge)
	}
	if !c.Audio.Playback || !c.Audio.Mic || !c.Clipboard {
		t.Errorf("features = %+v, want all enabled", c)
	}
}

func TestEdgeAcceptsEveryDirection(t *testing.T) {
	tests := map[string]proto.Edge{
		"left":   proto.EdgeLeft,
		"right":  proto.EdgeRight,
		"top":    proto.EdgeTop,
		"bottom": proto.EdgeBottom,
	}
	for name, want := range tests {
		t.Run(name, func(t *testing.T) {
			c, err := Load(writeConfig(t, "peer: pc\nedge: "+name+"\n"))
			if err != nil {
				t.Fatalf("Load: %v", err)
			}
			if c.Edge != want {
				t.Errorf("Edge = %v, want %v", c.Edge, want)
			}
		})
	}
}

// A typo here silently sends the cursor off the wrong side of the screen, so
// it has to be caught at load rather than discovered by feel.
func TestUnknownEdgeIsRejectedWithTheValidOnes(t *testing.T) {
	_, err := Load(writeConfig(t, "peer: pc\nedge: sideways\n"))

	if err == nil {
		t.Fatal("err = nil, want an error for an unknown edge")
	}
	for _, valid := range []string{"left", "right", "top", "bottom"} {
		if !strings.Contains(err.Error(), valid) {
			t.Errorf("err = %q, want it to list %q as a valid edge", err, valid)
		}
	}
}

// Without a peer there is nothing to connect to, and a default would be a
// guess at someone else's machine.
func TestMissingPeerIsRejected(t *testing.T) {
	_, err := Load(writeConfig(t, "edge: left\n"))

	if err == nil {
		t.Fatal("err = nil, want an error when peer is unset")
	}
	if !strings.Contains(err.Error(), "peer") {
		t.Errorf("err = %q, want it to name the missing field", err)
	}
}

func TestDefaultsFillInTheOptionalFields(t *testing.T) {
	c, err := Load(writeConfig(t, "peer: pc\nedge: left\n"))
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if c.Port != DefaultPort {
		t.Errorf("Port = %d, want the default %d", c.Port, DefaultPort)
	}
	if c.Socket != DefaultSocket {
		t.Errorf("Socket = %q, want the default %q", c.Socket, DefaultSocket)
	}
}

func TestMissingConfigFileSaysWhereItLooked(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "absent.yaml")

	_, err := Load(missing)

	if err == nil {
		t.Fatal("err = nil, want an error")
	}
	if !strings.Contains(err.Error(), missing) {
		t.Errorf("err = %q, want it to name the path", err)
	}
}

func TestMalformedYAMLIsReported(t *testing.T) {
	_, err := Load(writeConfig(t, "peer: [unclosed\n"))

	if err == nil {
		t.Fatal("err = nil, want a parse error")
	}
}

// A port below 1024 needs root to bind, which the unprivileged session does
// not have — better to say so than to fail at bind time.
func TestPrivilegedPortIsRejected(t *testing.T) {
	_, err := Load(writeConfig(t, "peer: pc\nedge: left\nport: 80\n"))

	if err == nil {
		t.Fatal("err = nil, want an error for a privileged port")
	}
	if !strings.Contains(err.Error(), "1024") {
		t.Errorf("err = %q, want it to explain the limit", err)
	}
}

func TestOutOfRangePortIsRejected(t *testing.T) {
	if _, err := Load(writeConfig(t, "peer: pc\nedge: left\nport: 70000\n")); err == nil {
		t.Error("err = nil, want an error for a port above 65535")
	}
}

// ---- pre-shared key ----
