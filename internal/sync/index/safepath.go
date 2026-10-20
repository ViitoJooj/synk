package index

import (
	"errors"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// ErrUnsafePath is a path from the other machine that would land outside the
// folder, or somewhere this machine cannot or must not write.
var ErrUnsafePath = errors.New("index: unsafe path")

// windowsReserved are the device names Windows resolves anywhere in a path,
// with any extension: "nul.txt" is the null device, not a file.
var windowsReserved = map[string]bool{
	"con": true, "prn": true, "aux": true, "nul": true,
	"com1": true, "com2": true, "com3": true, "com4": true, "com5": true,
	"com6": true, "com7": true, "com8": true, "com9": true,
	"lpt1": true, "lpt2": true, "lpt3": true, "lpt4": true, "lpt5": true,
	"lpt6": true, "lpt7": true, "lpt8": true, "lpt9": true,
}

// CheckRel refuses a relative path that could escape the folder or that one of
// the two systems cannot hold. It is applied to every path that arrives from
// the peer before anything is done with it, on both systems alike: a name
// Windows cannot store is refused on Linux too, or the two sides could never
// agree on it.
func CheckRel(rel string) error {
	bad := func(why string) error { return fmt.Errorf("%w %q: %s", ErrUnsafePath, rel, why) }

	switch {
	case rel == "" || rel == ".":
		return bad("empty")
	case strings.ContainsRune(rel, 0):
		return bad("contains NUL")
	case strings.ContainsRune(rel, '\\'):
		return bad("contains a backslash")
	case strings.HasPrefix(rel, "/"):
		return bad("absolute")
	case path.Clean(rel) != rel:
		return bad("not in clean form")
	}

	for _, seg := range strings.Split(rel, "/") {
		switch {
		case seg == "..":
			return bad("walks out of the folder")
		case strings.ContainsAny(seg, `:*?"<>|`):
			// ':' is also how Windows names an alternate data stream, and
			// "C:" a drive: either way, not a file in this folder.
			return bad("contains a character Windows forbids")
		case strings.HasSuffix(seg, ".") || strings.HasSuffix(seg, " "):
			return bad("ends in a dot or a space, which Windows drops")
		}
		for _, r := range seg {
			if r < 0x20 {
				return bad("contains a control character")
			}
		}
		base := strings.ToLower(seg)
		if i := strings.IndexByte(base, '.'); i >= 0 {
			base = base[:i]
		}
		if windowsReserved[base] {
			return bad("is a device name on Windows")
		}
	}
	return nil
}

// Join resolves rel under root for writing, after CheckRel, and refuses if any
// directory on the way is a symbolic link.
//
// The link check is the one that matters most. The scan never follows links,
// but a write would: with a local link "a" pointing at /etc, a peer sending
// "a/passwd" would write straight through it. Every existing component between
// root and the target is Lstat'ed; a missing one is fine, since it is about
// to be created as a plain directory.
func Join(root, rel string) (string, error) {
	if err := CheckRel(rel); err != nil {
		return "", err
	}
	root = filepath.Clean(root)
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if !strings.HasPrefix(abs, root+string(filepath.Separator)) {
		return "", fmt.Errorf("%w %q: escapes the folder", ErrUnsafePath, rel)
	}

	cur := root
	segs := strings.Split(rel, "/")
	for _, seg := range segs[:len(segs)-1] {
		cur = filepath.Join(cur, seg)
		info, err := os.Lstat(cur)
		if errors.Is(err, os.ErrNotExist) {
			break
		}
		if err != nil {
			return "", err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return "", fmt.Errorf("%w %q: %s is a symbolic link", ErrUnsafePath, rel, cur)
		}
		if !info.IsDir() {
			return "", fmt.Errorf("%w %q: %s is a file, not a directory", ErrUnsafePath, rel, cur)
		}
	}
	// The target itself must not be a link either: replacing one would write
	// through it on some systems, and it is not something the scan reported.
	if info, err := os.Lstat(abs); err == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("%w %q: is a symbolic link here", ErrUnsafePath, rel)
	}
	return abs, nil
}
