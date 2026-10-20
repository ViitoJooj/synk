package plan

import (
	"sort"
	"strings"

	"github.com/ViitoJooj/synk/internal/sync/index"
)

// repoRoots lists every git repository on either side, by the directory that
// holds its .git, longest first so the innermost one matches first.
func repoRoots(sides ...map[string]*index.Entry) []string {
	seen := map[string]bool{}
	for _, entries := range sides {
		for rel, e := range entries {
			if !e.Dir {
				continue
			}
			switch {
			case rel == ".git":
				seen[""] = true
			case strings.HasSuffix(rel, "/.git"):
				seen[strings.TrimSuffix(rel, "/.git")] = true
			}
		}
	}
	out := make([]string, 0, len(seen))
	for r := range seen {
		out = append(out, r)
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// repoOf is the innermost repository rel belongs to.
func repoOf(rel string, repos []string) (string, bool) {
	for _, r := range repos {
		if r == "" || rel == r || strings.HasPrefix(rel, r+"/") {
			return r, true
		}
	}
	return "", false
}

// divergentRepos are repositories present on both sides with any file that
// differs. Merging those file by file, as the rest of a first sync does,
// would leave "HEAD (samsung-book)" inside .git and a repository neither
// side's git understands; they wait for the user to pick a side, or to align
// them with git first.
func divergentRepos(in Input, repos []string) map[string]bool {
	out := map[string]bool{}
	for _, repo := range repos {
		gitDir := ".git"
		if repo != "" {
			gitDir = repo + "/.git"
		}
		if in.Local.Entries[gitDir] == nil || in.Remote.Entries[gitDir] == nil {
			continue // on one side only: it simply gets copied
		}
		for rel, l := range in.Local.Entries {
			r := in.Remote.Entries[rel]
			if r == nil || index.Same(l, r) {
				continue
			}
			if owner, ok := repoOf(rel, repos); ok && owner == repo {
				out[repo] = true
				break
			}
		}
	}
	return out
}
