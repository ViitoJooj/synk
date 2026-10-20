// Package config loads synk's settings.
//
// Only the machine that receives input needs a file: it is the one that knows
// the screen geometry and which border the peer sits beyond. The other side
// learns both over the wire, so it runs with nothing on disk at all.
package config

import (
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/ViitoJooj/synk/internal/proto"
)

const (
	// DefaultPort deliberately avoids 24800.
	//
	// That is the Synergy/Deskflow port, and sharing it is not a convenience:
	// a running Deskflow accepts the connection and then waits for a Synergy
	// handshake we do not speak, so the link fails with a read timeout that
	// blames the network. 27431 is unregistered in /etc/services and sits
	// below the ephemeral range on both Linux (32768+) and Windows (49152+).
	DefaultPort = 27431

	DefaultSocket = "/run/synk/injector.sock"
)

type Audio struct {
	Playback bool `yaml:"playback" json:"playback"`
	Mic      bool `yaml:"mic" json:"mic"`
}

type Config struct {
	Peer      string     `yaml:"peer"`
	Port      int        `yaml:"port"`
	Edge      proto.Edge `yaml:"-"`
	Socket    string     `yaml:"socket"`
	Audio     Audio      `yaml:"audio"`
	Clipboard bool       `yaml:"clipboard"`

	// EdgeName is what the file carries; Edge is the parsed form.
	EdgeName string `yaml:"edge"`
}

// edges maps the config file's words to the wire's edges.
var edges = map[string]proto.Edge{
	"left":   proto.EdgeLeft,
	"right":  proto.EdgeRight,
	"top":    proto.EdgeTop,
	"bottom": proto.EdgeBottom,
}

func Load(path string) (*Config, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("config: reading %s: %w", path, err)
	}

	var c Config
	if err := yaml.Unmarshal(raw, &c); err != nil {
		return nil, fmt.Errorf("config: parsing %s: %w", path, err)
	}

	if err := c.applyDefaults(); err != nil {
		return nil, err
	}
	return &c, c.validate()
}

func (c *Config) applyDefaults() error {
	if c.Port == 0 {
		c.Port = DefaultPort
	}
	if c.Socket == "" {
		c.Socket = DefaultSocket
	}

	edge, ok := edges[strings.ToLower(c.EdgeName)]
	if !ok {
		return fmt.Errorf("config: edge %q is not one of %s", c.EdgeName, edgeNames())
	}
	c.Edge = edge
	return nil
}

func (c *Config) validate() error {
	if c.Peer == "" {
		return errors.New("config: peer is required — the tailnet name of the other machine")
	}
	// The session runs unprivileged, so it could never bind a low port; saying
	// so here beats an opaque permission error at bind time.
	if c.Port < 1024 {
		return fmt.Errorf("config: port %d is privileged; use 1024 or above", c.Port)
	}
	if c.Port > 65535 {
		return fmt.Errorf("config: port %d is above the maximum of 65535", c.Port)
	}
	return nil
}

func edgeNames() string {
	names := make([]string, 0, len(edges))
	for name := range edges {
		names = append(names, name)
	}
	sort.Strings(names)
	return strings.Join(names, ", ")
}
