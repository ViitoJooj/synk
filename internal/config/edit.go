package config

import (
	"errors"
	"fmt"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Key is one setting `synk config` reads and writes.
type Key struct {
	Name string
	Help string
	// parse turns what the user typed into the value the file should hold,
	// or says why it cannot.
	parse func(string) (any, error)
	get   func(*Config) any
}

// Keys are the settings, in the order `synk config ls` shows them. The socket
// is left out on purpose: it has to match the injector's unit, and changing
// it on one side only would silently cut the link.
var Keys = []Key{
	{"peer", "the machine to link with, as 'synk ls' names it", parseName, func(c *Config) any { return c.Peer }},
	{"edge", "the border of this screen the peer's screen sits beyond: " + edgeNames(), parseEdge, func(c *Config) any { return c.EdgeName }},
	{"port", "TCP and UDP port the link uses", parsePort, func(c *Config) any { return c.Port }},
	{"clipboard", "share the clipboard", parseBool, func(c *Config) any { return c.Clipboard }},
	{"audio.playback", "play this machine's sound on the peer's speakers", parseBool, func(c *Config) any { return c.Audio.Playback }},
	{"audio.mic", "use the peer's microphone here", parseBool, func(c *Config) any { return c.Audio.Mic }},
}

var ErrUnknownKey = errors.New("config: unknown key")

// Lookup finds a key by name.
func Lookup(name string) (Key, error) {
	for _, k := range Keys {
		if k.Name == name {
			return k, nil
		}
	}
	names := make([]string, len(Keys))
	for i, k := range Keys {
		names[i] = k.Name
	}
	return Key{}, fmt.Errorf("%w %q; the keys are %s", ErrUnknownKey, name, strings.Join(names, ", "))
}

// Get is the key's value in cfg, defaults applied, as the user would type it.
func (k Key) Get(cfg *Config) string { return fmt.Sprint(k.get(cfg)) }

// Set writes one setting into the config at path and leaves every other byte
// as it was: this is the user's file, with their comments and blank lines, and
// one setting should change one line of it. Round-tripping it through a YAML
// encoder would reflow all of that, so the line is edited in place, and the
// result is parsed back before it is written to make sure the edit landed
// where it was meant to.
//
// The file must already exist. A config without an edge is not one this
// machine can run with, so there is nothing sensible to create from one key.
func Set(path, name, input string) error {
	key, err := Lookup(name)
	if err != nil {
		return err
	}
	value, err := key.parse(input)
	if err != nil {
		return fmt.Errorf("config: %s: %w", name, err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("config: reading %s: %w", path, err)
	}

	scalar, err := yaml.Marshal(value)
	if err != nil {
		return fmt.Errorf("config: encoding %v: %w", value, err)
	}
	out, err := setLine(raw, strings.Split(name, "."), strings.TrimSpace(string(scalar)))
	if err == nil {
		err = landed(out, strings.Split(name, "."), value)
	}
	if err != nil {
		return fmt.Errorf("config: %s has a layout synk cannot edit safely (%v); set %s: %v there by hand", path, err, name, value)
	}
	return writeAtomic(path, out)
}

// valueLine matches `key: value  # comment`: the key, the value, and the
// padding and comment after it.
func valueLine(key string) *regexp.Regexp {
	return regexp.MustCompile(`^([ \t]*` + regexp.QuoteMeta(key) + `:)[ \t]*([^#\n]*?)(?:([ \t]+)(#.*))?[ \t]*$`)
}

// setLine edits a top-level key, or a key one level down such as audio.mic.
func setLine(raw []byte, path []string, scalar string) ([]byte, error) {
	lines := strings.SplitAfter(string(raw), "\n")
	if len(path) > 2 {
		return nil, fmt.Errorf("keys nest one level at most")
	}

	top := indexOf(lines, 0, len(lines), path[0], false)
	if len(path) == 1 {
		if top >= 0 {
			lines[top] = replaceValue(lines[top], path[0], scalar)
			return []byte(strings.Join(lines, "")), nil
		}
		at := firstTopLevel(lines)
		return []byte(strings.Join(insert(lines, at, path[0]+": "+scalar+"\n"), "")), nil
	}

	if top < 0 {
		text := strings.Join(lines, "")
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		return []byte(text + path[0] + ":\n  " + path[1] + ": " + scalar + "\n"), nil
	}

	// The block is every line after the parent that is indented, blank or a
	// comment, up to the next top-level key.
	end := top + 1
	lastChild, indent := top, "  "
	for ; end < len(lines); end++ {
		l := lines[end]
		trimmed := strings.TrimSpace(l)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		if l[0] != ' ' && l[0] != '\t' {
			break
		}
		if lastChild == top {
			indent = l[:len(l)-len(strings.TrimLeft(l, " \t"))]
		}
		lastChild = end
	}

	if child := indexOf(lines, top+1, end, path[1], true); child >= 0 {
		lines[child] = replaceValue(lines[child], path[1], scalar)
		return []byte(strings.Join(lines, "")), nil
	}
	if !strings.HasSuffix(lines[lastChild], "\n") {
		lines[lastChild] += "\n"
	}
	return []byte(strings.Join(insert(lines, lastChild+1, indent+path[1]+": "+scalar+"\n"), "")), nil
}

// indexOf finds `key:` among lines[from:to], indented or at the top level.
func indexOf(lines []string, from, to int, key string, indented bool) int {
	re := valueLine(key)
	for i := from; i < to; i++ {
		l := strings.TrimRight(lines[i], "\n")
		if l == "" || (l[0] == ' ' || l[0] == '\t') != indented {
			continue
		}
		if re.MatchString(l) {
			return i
		}
	}
	return -1
}

func replaceValue(line, key, scalar string) string {
	nl := ""
	if strings.HasSuffix(line, "\n") {
		line, nl = strings.TrimSuffix(line, "\n"), "\n"
	}
	m := valueLine(key).FindStringSubmatch(line)
	if m[4] == "" {
		return m[1] + " " + scalar + nl
	}
	// Keep a trailing comment in its column: people align them, and a value
	// that grows from true to false should not push it one step right.
	pad := len(m[3]) + len(m[2]) - len(scalar)
	return m[1] + " " + scalar + strings.Repeat(" ", max(pad, 1)) + m[4] + nl
}

// firstTopLevel is where a new top-level key goes: before the first one, so it
// lands under the file's header comment rather than after everything.
func firstTopLevel(lines []string) int {
	for i, l := range lines {
		if l != "" && l[0] != ' ' && l[0] != '\t' && l[0] != '#' && l[0] != '\n' {
			return i
		}
	}
	return len(lines)
}

func insert(lines []string, at int, line string) []string {
	if at > 0 && at == len(lines) && !strings.HasSuffix(lines[at-1], "\n") {
		lines[at-1] += "\n"
	}
	return append(lines[:at], append([]string{line}, lines[at:]...)...)
}

// landed parses the edited file and checks the key now holds the value.
func landed(raw []byte, path []string, want any) error {
	var doc map[string]any
	if err := yaml.Unmarshal(raw, &doc); err != nil {
		return err
	}
	var got any = doc
	for _, p := range path {
		m, ok := got.(map[string]any)
		if !ok {
			return fmt.Errorf("%s is not a mapping", p)
		}
		got = m[p]
	}
	if !reflect.DeepEqual(got, want) {
		return fmt.Errorf("the file reads back %v", got)
	}
	return nil
}

func parseName(s string) (any, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, errors.New("a machine name cannot be empty")
	}
	return s, nil
}

func parseEdge(s string) (any, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if _, ok := edges[s]; !ok {
		return nil, fmt.Errorf("%q is not one of %s", s, edgeNames())
	}
	return s, nil
}

func parsePort(s string) (any, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return nil, fmt.Errorf("%q is not a number", s)
	}
	if n < 1024 || n > 65535 {
		return nil, fmt.Errorf("%d is outside 1024-65535", n)
	}
	return n, nil
}

func parseBool(s string) (any, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "true", "on", "yes", "1":
		return true, nil
	case "false", "off", "no", "0":
		return false, nil
	}
	return nil, fmt.Errorf("%q is not on or off", s)
}

// Setting is one key and its current value, as `synk config ls` and the
// control API list them.
type Setting struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Help  string `json:"help"`
}

// List is every key's value in cfg, defaults applied.
func List(cfg *Config) []Setting {
	out := make([]Setting, len(Keys))
	for i, k := range Keys {
		out[i] = Setting{Key: k.Name, Value: k.Get(cfg), Help: k.Help}
	}
	return out
}
