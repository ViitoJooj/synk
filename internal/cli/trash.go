package cli

import (
	"errors"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/ViitoJooj/synk/internal/config"
	"github.com/ViitoJooj/synk/internal/sync/folders"
	"github.com/ViitoJooj/synk/internal/sync/trash"
	"github.com/ViitoJooj/synk/pkg/errorx"
)

var trashForce bool

var trashCmd = &cobra.Command{
	Use:   "trash",
	Short: "What the sync deleted or overwrote, kept for 30 days",
	Long: `Every file the sync deletes, or overwrites with the peer's version, is
moved here first and kept for 30 days. 'synk trash restore' puts one back.`,
}

var trashLsCmd = &cobra.Command{
	Use:     "ls [folder]",
	Aliases: []string{"list"},
	Short:   "List what is in the trash, newest first",
	Args:    cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		t, err := openTrash()
		if err != nil {
			return err
		}
		folder := ""
		if len(args) == 1 {
			folder = args[0]
		}
		items, err := t.List(folder)
		if err != nil {
			return errorx.Failed("read the trash", err)
		}
		rows := make([][]string, 0, len(items))
		for _, it := range items {
			rows = append(rows, []string{it.ID, size(it.Size), since(it.Trashed, time.Now()) + " ago"})
		}
		if err := printList(cmd.OutOrStdout(), items, []string{"ID", "SIZE", "TRASHED"}, rows); err != nil {
			return err
		}
		if len(items) > 0 {
			say(cmd.OutOrStdout(), "\nput one back with: synk trash restore <id>")
		}
		return nil
	},
}

var trashRestoreCmd = &cobra.Command{
	Use:   "restore <id>",
	Short: "Put a trashed file back where it was",
	Long: `Put a trashed file back where it was, in its folder.

If a file is there now, nothing is overwritten unless --force is given, and
then the one there goes to the trash in turn.`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		t, err := openTrash()
		if err != nil {
			return err
		}
		list, err := folders.LoadDefault()
		if err != nil {
			return folderError("read the folder list", err)
		}
		name, _, _ := strings.Cut(args[0], "/")
		f, err := list.Get(name)
		if err != nil {
			return folderError("restore "+args[0], err)
		}
		dest, err := t.Restore(args[0], f.Path, trashForce)
		if errors.Is(err, trash.ErrExists) {
			return errorx.Folder("restore "+args[0], "add --force to put it back anyway; the file there now goes to the trash", err)
		}
		if err != nil {
			return errorx.Failed("restore "+args[0], err)
		}
		say(cmd.OutOrStdout(), "restored to %s", dest)
		return nil
	},
}

func openTrash() (*trash.Trash, error) {
	dir, err := config.TrashDir()
	if err != nil {
		return nil, errorx.Failed("locate the trash", err)
	}
	return trash.New(dir), nil
}

func init() {
	trashRestoreCmd.Flags().BoolVar(&trashForce, "force", false, "put it back even if a file is there now")
	trashCmd.AddCommand(trashLsCmd, trashRestoreCmd)
	rootCmd.AddCommand(trashCmd)
}
