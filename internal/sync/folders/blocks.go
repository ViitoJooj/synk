package folders

import (
	"fmt"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
)

// Junk is never synced and cannot be unblocked: files the systems themselves
// scatter, and Office's lock files, which name a document's owner and vanish
// when it closes.
var Junk = []string{"Thumbs.db", "desktop.ini", ".DS_Store", "$RECYCLE.BIN", "System Volume Information", "~$*"}

// IsPattern tells a pattern from a path: a pattern has no separator and does
// not start with "~" or ".", so "node_modules" and "*.env" are patterns and
// "./secrets", "~/keys" and "/srv/x" are paths.
func IsPattern(s string) bool {
	return !strings.ContainsAny(s, `/\`) && !strings.HasPrefix(s, "~") && s != "." && s != ".."
}

// Block adds a pattern, or a path after resolving it. It returns what was
// stored.
func (f *File) Block(what string) (string, error) {
	entry := what
	if !IsPattern(what) {
		abs, err := Resolve(what)
		if err != nil {
			return "", err
		}
		entry = abs
	} else if _, err := path.Match(what, ""); err != nil {
		return "", fmt.Errorf("folders: %q is not a valid pattern: %w", what, err)
	}
	for _, b := range f.Blocks {
		if b == entry {
			return "", fmt.Errorf("%w: %s", ErrBlocked, entry)
		}
	}
	f.Blocks = append(f.Blocks, entry)
	return entry, nil
}

// Unblock removes a block. Built-in ones cannot be removed.
func (f *File) Unblock(what string) (string, error) {
	entry := what
	if !IsPattern(what) {
		abs, err := Resolve(what)
		if err != nil {
			return "", err
		}
		entry = abs
	}
	for _, j := range Junk {
		if j == entry {
			return "", fmt.Errorf("%w: %s", ErrBuiltIn, entry)
		}
	}
	for _, h := range HardBlocks() {
		if within(entry, h) {
			return "", fmt.Errorf("%w: %s is under %s", ErrBuiltIn, entry, h)
		}
	}
	for i, b := range f.Blocks {
		if b == entry {
			f.Blocks = append(f.Blocks[:i], f.Blocks[i+1:]...)
			return entry, nil
		}
	}
	return "", fmt.Errorf("%w: %s is not blocked", ErrNotFound, entry)
}

func (f *File) pathBlocks() []string {
	var out []string
	for _, b := range f.Blocks {
		if !IsPattern(b) {
			out = append(out, b)
		}
	}
	return out
}

// Rules are what stays out of one folder. They travel to the peer, which
// applies them too: a block holds in both directions.
type Rules struct {
	// Patterns match a name anywhere in the folder.
	Patterns []string `json:"patterns,omitempty"`
	// Paths are relative to the folder's root, with forward slashes.
	Paths []string `json:"paths,omitempty"`
}

// Rules for a folder: every pattern, and the path blocks that fall inside it.
func (f *File) Rules(folder Folder) Rules {
	r := Rules{Patterns: append([]string(nil), Junk...)}
	for _, b := range f.Blocks {
		if IsPattern(b) {
			r.Patterns = append(r.Patterns, b)
			continue
		}
		if within(b, folder.Path) && b != folder.Path {
			rel, err := filepath.Rel(folder.Path, b)
			if err == nil {
				r.Paths = append(r.Paths, filepath.ToSlash(rel))
			}
		}
	}
	return r.normal()
}

// Merge is the union of two sides' rules.
func Merge(a, b Rules) Rules {
	return Rules{
		Patterns: append(append([]string(nil), a.Patterns...), b.Patterns...),
		Paths:    append(append([]string(nil), a.Paths...), b.Paths...),
	}.normal()
}

func (r Rules) normal() Rules {
	return Rules{Patterns: dedupe(r.Patterns), Paths: dedupe(r.Paths)}
}

// Excluded reports whether rel is blocked. The planner also checks every
// directory on the way, so a block on a directory covers what is inside.
func (r Rules) Excluded(rel string, _ bool) bool {
	for _, p := range r.Paths {
		if rel == p || strings.HasPrefix(rel, p+"/") {
			return true
		}
	}
	name := strings.ToLower(path.Base(rel))
	for _, pat := range r.Patterns {
		if ok, _ := path.Match(strings.ToLower(pat), name); ok {
			return true
		}
	}
	return false
}

func dedupe(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	sort.Strings(out)
	return out
}

// HardBlocks are paths no folder may be, or hold: keys, keyrings, browser
// profiles with their saved passwords and cookies, synk's own state, the
// system. A folder inside one, or around one, is refused. They cannot be
// unblocked.
func HardBlocks() []string {
	home, _ := os.UserHomeDir()
	var out []string
	add := func(p ...string) {
		for _, x := range p {
			if x != "" {
				out = append(out, filepath.Clean(x))
			}
		}
	}
	j := func(parts ...string) string {
		if home == "" {
			return ""
		}
		return filepath.Join(append([]string{home}, parts...)...)
	}

	switch runtime.GOOS {
	case "windows":
		sys := os.Getenv("SystemRoot")
		if sys == "" {
			sys = `C:\Windows`
		}
		add(sys, os.Getenv("ProgramFiles"), os.Getenv("ProgramFiles(x86)"), os.Getenv("ProgramData"))
		add(j(".ssh"), j(".gnupg"))
		if appdata := os.Getenv("APPDATA"); appdata != "" {
			add(filepath.Join(appdata, "synk"), filepath.Join(appdata, "Mozilla"), filepath.Join(appdata, "Microsoft"))
		}
		if local := os.Getenv("LOCALAPPDATA"); local != "" {
			add(filepath.Join(local, "synk"), filepath.Join(local, "Google"), filepath.Join(local, "Microsoft"),
				filepath.Join(local, "BraveSoftware"))
		}
	default:
		add("/etc", "/usr", "/boot", "/var", "/proc", "/sys", "/dev", "/run", "/root")
		add(j(".ssh"), j(".gnupg"), j(".password-store"), j(".pki"), j(".mozilla"),
			j(".local", "share", "keyrings"), j(".local", "share", "synk"), j(".local", "state", "synk"),
			j(".config", "synk"), j(".config", "google-chrome"), j(".config", "chromium"),
			j(".config", "BraveSoftware"), j(".config", "Code", "User", "globalStorage"))
	}
	return out
}

// hardBlockHit returns the hard block p is inside of, or holds, or "".
func hardBlockHit(p string) string {
	for _, h := range HardBlocks() {
		if within(p, h) || within(h, p) {
			return h
		}
	}
	return ""
}

// BlockInfo is one block as 'synk block ls' shows it.
type BlockInfo struct {
	What string `json:"what"`
	// Kind is "pattern" or "path".
	Kind string `json:"kind"`
	// Origin is "yours", "default" (a build-output pattern the list starts
	// with) or "built-in" (cannot be removed).
	Origin string `json:"origin"`
}

// BlockList is every block in force: the list's, the junk patterns and the
// paths no folder may touch.
func (f *File) BlockList() []BlockInfo {
	defaults := map[string]bool{}
	for _, d := range DefaultBlocks {
		defaults[d] = true
	}
	var out []BlockInfo
	for _, b := range f.Blocks {
		info := BlockInfo{What: b, Kind: "path", Origin: "yours"}
		if IsPattern(b) {
			info.Kind = "pattern"
			if defaults[b] {
				info.Origin = "default"
			}
		}
		out = append(out, info)
	}
	for _, j := range Junk {
		out = append(out, BlockInfo{What: j, Kind: "pattern", Origin: "built-in"})
	}
	for _, h := range HardBlocks() {
		out = append(out, BlockInfo{What: h, Kind: "path", Origin: "built-in"})
	}
	return out
}
