package config

import (
	"errors"
	"os"
	"strings"
	"testing"
)

// realConfig is this machine's own file, header and alignment included,
// because that is what the editor has to leave alone.
const realConfig = `# notebook Linux <-> PC Windows sobre o tailnet.
#
# Só esta máquina precisa de arquivo de configuração.
peer: desktop-6v4goh8

# Porta omitida de propósito: o padrão é 27431.

edge: right

audio:
  playback: true   # o que toca aqui sai nas caixas do PC
  mic: true        # o mic do PC vira fonte virtual aqui

clipboard: true
`

func setAndRead(t *testing.T, body, key, value string) string {
	t.Helper()
	path := writeConfig(t, body)
	if err := Set(path, key, value); err != nil {
		t.Fatalf("Set(%s, %s): %v", key, value, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := Load(path); err != nil {
		t.Fatalf("the edited file no longer loads: %v\n%s", err, raw)
	}
	return string(raw)
}

func TestSetNestedKeyChangesOneLine(t *testing.T) {
	got := setAndRead(t, realConfig, "audio.mic", "off")

	want := strings.Replace(realConfig,
		"  mic: true        # o mic do PC vira fonte virtual aqui",
		"  mic: false       # o mic do PC vira fonte virtual aqui", 1)
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestSetKeepsAtLeastOneSpaceBeforeAComment(t *testing.T) {
	got := setAndRead(t, "peer: a # x\nedge: left\n", "peer", "desktop-6v4goh8")
	if !strings.HasPrefix(got, "peer: desktop-6v4goh8 # x\n") {
		t.Errorf("got %q", got)
	}
}

func TestSetAddsAMissingTopLevelKeyUnderTheHeader(t *testing.T) {
	got := setAndRead(t, realConfig, "port", "27500")

	if !strings.Contains(got, "# Só esta máquina precisa de arquivo de configuração.\nport: 27500\npeer: desktop-6v4goh8\n") {
		t.Errorf("port not placed before the first setting:\n%s", got)
	}
}

func TestSetAddsAMissingChildToItsBlock(t *testing.T) {
	body := "peer: pc\nedge: left\naudio:\n    playback: true\nclipboard: true\n"
	got := setAndRead(t, body, "audio.mic", "yes")

	want := "peer: pc\nedge: left\naudio:\n    playback: true\n    mic: true\nclipboard: true\n"
	if got != want {
		t.Errorf("got\n%s\nwant\n%s", got, want)
	}
}

func TestSetAddsAMissingBlock(t *testing.T) {
	got := setAndRead(t, "peer: pc\nedge: left", "audio.playback", "on")

	if got != "peer: pc\nedge: left\naudio:\n  playback: true\n" {
		t.Errorf("got %q", got)
	}
}

// A key of the same name in another block is a different setting.
func TestSetFindsTheChildInTheRightBlock(t *testing.T) {
	body := "peer: pc\nedge: left\nother:\n  mic: true\naudio:\n  mic: true\n"
	got := setAndRead(t, body, "audio.mic", "off")

	if got != "peer: pc\nedge: left\nother:\n  mic: true\naudio:\n  mic: false\n" {
		t.Errorf("got %q", got)
	}
}

func TestSetValidatesBeforeTouchingTheFile(t *testing.T) {
	cases := map[string]string{
		"edge":      "diagonal",
		"port":      "80",
		"clipboard": "maybe",
		"peer":      "  ",
	}
	for key, value := range cases {
		path := writeConfig(t, realConfig)
		if err := Set(path, key, value); err == nil {
			t.Errorf("Set(%s, %q) accepted", key, value)
		}
		if raw, _ := os.ReadFile(path); string(raw) != realConfig {
			t.Errorf("Set(%s, %q) changed the file on failure", key, value)
		}
	}
}

func TestSetRejectsAnUnknownKey(t *testing.T) {
	err := Set(writeConfig(t, realConfig), "socket", "/tmp/x")
	if !errors.Is(err, ErrUnknownKey) || !strings.Contains(err.Error(), "audio.mic") {
		t.Errorf("err = %v, want the list of keys", err)
	}
}

// An inline mapping cannot be edited line by line; the check that reads the
// file back has to catch it instead of writing something wrong.
func TestSetRefusesAnInlineMapping(t *testing.T) {
	path := writeConfig(t, "peer: pc\nedge: left\naudio: {playback: true}\n")

	err := Set(path, "audio.mic", "on")
	if err == nil || !strings.Contains(err.Error(), "by hand") {
		t.Fatalf("err = %v, want a refusal", err)
	}
}

func TestKeysReadTheDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, "peer: pc\nedge: LEFT\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"peer": "pc", "edge": "LEFT", "port": "27431",
		"clipboard": "false", "audio.playback": "false", "audio.mic": "false",
	}
	for _, k := range Keys {
		if got := k.Get(cfg); got != want[k.Name] {
			t.Errorf("%s = %q, want %q", k.Name, got, want[k.Name])
		}
	}
}
