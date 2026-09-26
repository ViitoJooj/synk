package cli

import (
	"github.com/spf13/cobra"

	"github.com/ViitoJooj/synk/internal/sync/folders"
)

var blockCmd = &cobra.Command{
	Use:   "block <path|pattern>",
	Short: "Keep a path, or every file matching a pattern, out of the sync",
	Long: `Keep something out of the sync, in both directions.

A pattern has no slash and matches a file or directory name anywhere in any
folder: node_modules, *.env, *.pem. A path starts with ".", "~" or "/" (or a
drive on Windows): ./secrets, ~/Documentos/codes/proj/keys.

Blocking a path inside a synced folder holds on both machines: nothing under
it is sent from here, and nothing that arrives from the peer is written into
it. Blocking a path outside every folder keeps any folder inside it from
being added.

Blocking deletes nothing: files already there stay, and stop syncing.

To block a file literally named "ls", write it as a path: ./ls`,
	Example: `  synk block '*.env'
  synk block ./proj/secrets
  synk block ls`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var stored string
		err := folders.Edit(func(f *folders.File) error {
			var err error
			stored, err = f.Block(args[0])
			return err
		})
		if err != nil {
			return folderError("block "+args[0], err)
		}
		kick()
		say(cmd.OutOrStdout(), "blocked %s", stored)
		return nil
	},
}

var unblockCmd = &cobra.Command{
	Use:   "unblock <path|pattern>",
	Short: "Let a blocked path or pattern sync again",
	Args:  cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		var stored string
		err := folders.Edit(func(f *folders.File) error {
			var err error
			stored, err = f.Unblock(args[0])
			return err
		})
		if err != nil {
			return folderError("unblock "+args[0], err)
		}
		kick()
		say(cmd.OutOrStdout(), "unblocked %s", stored)
		return nil
	},
}

var blockLsCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "List every block, including the built-in ones",
	Args:    cobra.NoArgs,
	RunE: func(cmd *cobra.Command, _ []string) error {
		list, err := folders.LoadDefault()
		if err != nil {
			return folderError("read the block list", err)
		}
		blocks := list.BlockList()
		rows := make([][]string, 0, len(blocks))
		for _, b := range blocks {
			rows = append(rows, []string{b.What, b.Kind, originWord(b.Origin)})
		}
		return printList(cmd.OutOrStdout(), blocks, []string{"BLOCKED", "KIND", "ORIGIN"}, rows)
	},
}

func originWord(o string) string {
	switch o {
	case "built-in":
		return "built in, cannot be removed"
	case "default":
		return "default (build output)"
	}
	return "yours"
}

func init() {
	blockCmd.AddCommand(blockLsCmd)
	rootCmd.AddCommand(blockCmd, unblockCmd)
}
