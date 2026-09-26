package cli

import (
	"errors"
	"fmt"
	"sort"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/ViitoJooj/synk/internal/control"
	"github.com/ViitoJooj/synk/internal/sync/engine"
	"github.com/ViitoJooj/synk/internal/sync/folders"
	"github.com/ViitoJooj/synk/pkg/errorx"
)

var folderCmd = &cobra.Command{
	Use:     "folder",
	Aliases: []string{"folders"},
	Short:   "Sync folders with the peer",
	Long: `Keep folders the same on both machines.

A folder is paired by name: add it on each machine with the same name,
wherever each keeps it. The first time, synk compares the two copies and
writes nothing until you accept on each side; that first sync deletes
nothing, and files that differ are kept in both versions.

After that, changes flow both ways. Whatever the sync deletes or overwrites
goes to 'synk trash' first.`,
	Example: `  synk folder add ~/Documentos/codes --as codes
  synk folder show codes
  synk folder accept codes`,
}

var (
	folderAs            string
	folderAcceptDeletes bool
	folderKeepHere      []string
	folderTakePeer      []string
	folderOffline       bool
)

var folderAddCmd = &cobra.Command{
	Use:   "add <path>",
	Short: "Start syncing a folder",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var added folders.Folder
		err := folders.Edit(func(f *folders.File) error {
			got, err := f.Add(args[0], folderAs)
			if err == nil {
				added = *got
			}
			return err
		})
		if err != nil {
			return folderError("add "+args[0], err)
		}
		kick()
		out := cmd.OutOrStdout()
		say(out, "added %s as %q", added.Path, added.Name)
		say(out, "next: add it on the other machine with the same name (synk folder add <path> --as %s),", added.Name)
		say(out, "then review with 'synk folder show %s' and accept on both sides. Nothing is written before that.", added.Name)
		return nil
	},
}

var folderRmCmd = &cobra.Command{
	Use:     "rm <name>",
	Aliases: []string{"remove"},
	Short:   "Stop syncing a folder; its files stay where they are",
	Args:    cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := folders.Edit(func(f *folders.File) error { return f.Remove(args[0]) }); err != nil {
			return folderError("remove "+args[0], err)
		}
		kick()
		say(cmd.OutOrStdout(), "%s is no longer synced; no file was touched", args[0])
		return nil
	},
}

var folderAcceptCmd = &cobra.Command{
	Use:   "accept <name>",
	Short: "Let a folder's first sync run, after reviewing it",
	Long: `Let a folder's first sync run.

Run 'synk folder show <name>' first: it lists what comes, what goes, and the
files that exist on both sides with different content, which are kept in both
versions ("README (samsung-book).md").

A git repository that exists on both sides with different content is held,
because merging it file by file would break it. Pick a side for it: the
other side's differing files go to its trash. Push from the side that loses
first, and pull on the side that wins, so no commit is left behind. (Pushing
and pulling alone never unblocks it: git's own files — the index, the logs —
differ between two clones even with the same commits.)

  --keep-here proj    this machine's version of every differing file in proj
  --take-peer proj    the other machine's

Use opposite choices on the two machines: --keep-here on one, --take-peer on
the other.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		prefer := map[string]string{}
		for _, r := range folderKeepHere {
			prefer[r] = "local"
		}
		for _, r := range folderTakePeer {
			if prefer[r] == "local" {
				return folderError("accept", fmt.Errorf("%s is both kept here and taken from the peer", r))
			}
			prefer[r] = "peer"
		}
		if err := folders.Edit(func(f *folders.File) error { return f.Accept(args[0], prefer) }); err != nil {
			return folderError("accept "+args[0], err)
		}
		kick()
		say(cmd.OutOrStdout(), "%s accepted: the first sync runs now on this side. It deletes nothing, and keeps both versions of what differs.", args[0])
		return nil
	},
}

var folderPauseCmd = &cobra.Command{
	Use:   "pause <name>",
	Short: "Stop syncing a folder for now",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if err := folders.Edit(func(f *folders.File) error { return f.Pause(args[0]) }); err != nil {
			return folderError("pause "+args[0], err)
		}
		kick()
		say(cmd.OutOrStdout(), "%s paused", args[0])
		return nil
	},
}

var folderResumeCmd = &cobra.Command{
	Use:   "resume <name>",
	Short: "Resume a paused folder",
	Long: `Resume a paused folder.

A folder the mass-deletion guard stopped resumes with --accept-deletes, once
you have checked that the deletions it stopped are the ones you meant. They
go to 'synk trash', as every deletion does.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		err := folders.Edit(func(f *folders.File) error { return f.Resume(args[0], folderAcceptDeletes) })
		if err != nil {
			return folderError("resume "+args[0], err)
		}
		kick()
		say(cmd.OutOrStdout(), "%s resumed", args[0])
		return nil
	},
}

var folderLsCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List synced folders and where each stands",
	Args:    cobra.NoArgs,
	RunE:    runFolderLs,
}

var folderShowCmd = &cobra.Command{
	Use:   "show <name>",
	Short: "Everything about one folder: the first sync's preview, what is held or skipped",
	Args:  cobra.ExactArgs(1),
	RunE:  runFolderShow,
}

func init() {
	folderAddCmd.Flags().StringVar(&folderAs, "as", "", "the name that pairs it with the peer's folder (default: the directory's name)")
	folderResumeCmd.Flags().BoolVar(&folderAcceptDeletes, "accept-deletes", false, "let the deletions the guard stopped go ahead")
	folderAcceptCmd.Flags().StringSliceVar(&folderKeepHere, "keep-here", nil, "a repository to settle with this machine's version")
	folderAcceptCmd.Flags().StringSliceVar(&folderTakePeer, "take-peer", nil, "a repository to settle with the peer's version")
	folderLsCmd.Flags().BoolVar(&folderOffline, "offline", false, "list from folders.yaml, without asking the agent")
	folderCmd.AddCommand(folderAddCmd, folderRmCmd, folderLsCmd, folderShowCmd,
		folderAcceptCmd, folderPauseCmd, folderResumeCmd)
	rootCmd.AddCommand(folderCmd)
}

// kick tells a running agent the folder list changed, so it acts now rather
// than at its next scan. Without an agent there is nobody to tell.
func kick() { _ = call("folders.kick", nil, nil) }

// syncStatus is the agent's view of the folders.
func syncStatus() (*engine.Status, error) {
	var st engine.Status
	err := call("folders", nil, &st)
	if errors.Is(err, control.ErrNotRunning) {
		return nil, errorx.NotSyncing()
	}
	return &st, err
}

func runFolderLs(cmd *cobra.Command, _ []string) error {
	out := cmd.OutOrStdout()
	if folderOffline {
		list, err := folders.LoadDefault()
		if err != nil {
			return folderError("read the folder list", err)
		}
		rows := [][]string{}
		for _, f := range list.Folders {
			rows = append(rows, []string{f.Name, f.Path, acceptedWord(f)})
		}
		return printList(out, list.Folders, []string{"NAME", "PATH", "STATE"}, rows)
	}

	st, err := syncStatus()
	if err != nil {
		return err
	}
	if asJSON {
		return printJSON(out, st)
	}
	rows := [][]string{}
	for _, f := range st.Folders {
		rows = append(rows, []string{f.Name, f.Path, f.Phase, fmt.Sprint(f.Files), size(f.Bytes), folderDetail(f)})
	}
	if err := printList(out, st.Folders, []string{"NAME", "PATH", "STATE", "FILES", "SIZE", "DETAIL"}, rows); err != nil {
		return err
	}
	if len(st.Folders) == 0 {
		say(out, "no folders yet; add one with 'synk folder add <path>'")
	}
	for _, name := range st.Offered {
		say(out, "\nthe peer also syncs %q; to sync it here: synk folder add <path> --as %s", name, name)
	}
	if !st.Connected {
		say(out, "\nthe peer is not connected; folders wait for it")
	}
	return nil
}

func runFolderShow(cmd *cobra.Command, args []string) error {
	st, err := syncStatus()
	if err != nil {
		return err
	}
	var f *engine.FolderStatus
	for i := range st.Folders {
		if st.Folders[i].Name == args[0] {
			f = &st.Folders[i]
		}
	}
	if f == nil {
		return folderError("show "+args[0], fmt.Errorf("%w: %q", folders.ErrNotFound, args[0]))
	}
	out := cmd.OutOrStdout()
	if asJSON {
		return printJSON(out, f)
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	fmt.Fprintf(w, "folder\t%s\t%s\n", f.Name, f.Path)
	fmt.Fprintf(w, "state\t%s\t%s\n", f.Phase, f.Reason)
	fmt.Fprintf(w, "files\t%d\t%s\n", f.Files, size(f.Bytes))
	if f.LastSync != nil {
		fmt.Fprintf(w, "in sync\t%s ago\t\n", since(*f.LastSync, time.Now()))
	}
	if f.Pending > 0 {
		fmt.Fprintf(w, "on the way\t%d files\t\n", f.Pending)
	}
	w.Flush()

	if pv := f.Preview; pv != nil {
		fmt.Fprintf(out, "\nFirst sync, if accepted:\n")
		fmt.Fprintf(out, "  comes from the peer  %d files (%s)\n", pv.Fetch, size(pv.FetchBytes))
		fmt.Fprintf(out, "  goes to the peer     %d files (%s)\n", pv.Send, size(pv.SendBytes))
		fmt.Fprintf(out, "  deleted              nothing\n")
		if len(pv.Conflicts) > 0 {
			list(out, "On both sides with different content, kept in both versions", pv.Conflicts, 20)
		} else {
			fmt.Fprintf(out, "  conflicts            none\n")
		}
	}
	if len(f.Divergent) > 0 {
		list(out, "Git repositories that differ on both sides, held", f.Divergent, 0)
		fmt.Fprintf(out, "  pick a side for each: --keep-here on one machine, --take-peer on the other ('synk folder accept --help')\n")
	}
	if len(f.Held) > 0 {
		list(out, "Held this round (git at work, or unreadable)", f.Held, 20)
	}
	if len(f.Skipped) > 0 {
		var lines []string
		for _, s := range f.Skipped {
			lines = append(lines, s.Path+": "+s.Why)
		}
		list(out, "Skipped", lines, 20)
	}
	return nil
}

func list(out interface{ Write([]byte) (int, error) }, title string, items []string, max int) {
	fmt.Fprintf(out, "\n%s:\n", title)
	sort.Strings(items)
	for i, it := range items {
		if max > 0 && i == max {
			fmt.Fprintf(out, "  ... and %d more (all of them with --json)\n", len(items)-max)
			break
		}
		fmt.Fprintf(out, "  %s\n", it)
	}
}

func folderDetail(f engine.FolderStatus) string {
	switch {
	case f.Phase == engine.PhasePreview && f.Preview != nil:
		return fmt.Sprintf("first sync: %d in, %d out, %d conflicts; 'synk folder show %s'",
			f.Preview.Fetch, f.Preview.Send, len(f.Preview.Conflicts), f.Name)
	case f.Pending > 0:
		return fmt.Sprintf("%d files on the way", f.Pending)
	case f.LastSync != nil && f.Phase == engine.PhaseIdle:
		return "in sync"
	}
	return f.Reason
}

func acceptedWord(f folders.Folder) string {
	switch {
	case f.Paused:
		return "paused"
	case !f.Accepted:
		return "not accepted yet"
	}
	return "accepted"
}

// size is a byte count a person reads.
func size(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for m := n / unit; m >= unit; m /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}

// folderError explains a refused change with the hint that fits it.
func folderError(action string, err error) error { return folders.Explain(action, err) }
