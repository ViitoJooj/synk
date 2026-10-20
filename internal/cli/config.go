package cli

import (
	"errors"
	"fmt"

	"github.com/spf13/cobra"

	"github.com/ViitoJooj/synk/internal/config"
	"github.com/ViitoJooj/synk/internal/control"
	"github.com/ViitoJooj/synk/pkg/errorx"
)

var configCmd = &cobra.Command{
	Use:   "config",
	Short: "Read and change settings, like git config",
	Long: `Read and change the settings in the config file.

Only the Linux machine has settings: it receives the input, so it is the one
that knows where the peer's screen is. 'synk config set' changes one line of
the file and leaves your comments where they were; the agent picks the change
up at once.`,
	Example: `  synk config ls
  synk config get edge
  synk config set audio.mic off`,
}

var configLsCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list"},
	Short:   "Show every setting and its value, defaults included",
	Args:    cobra.NoArgs,
	RunE:    runConfigLs,
}

var configGetCmd = &cobra.Command{
	Use:       "get <key>",
	Short:     "Print one setting",
	Args:      cobra.ExactArgs(1),
	ValidArgs: keyNames(),
	RunE:      runConfigGet,
}

var configSetCmd = &cobra.Command{
	Use:       "set <key> <value>",
	Short:     "Change one setting",
	Args:      cobra.ExactArgs(2),
	ValidArgs: keyNames(),
	RunE:      runConfigSet,
}

func init() {
	configCmd.AddCommand(configLsCmd, configGetCmd, configSetCmd)
	rootCmd.AddCommand(configCmd)
}

func keyNames() []string {
	names := make([]string, len(config.Keys))
	for i, k := range config.Keys {
		names[i] = k.Name
	}
	return names
}

// readConfig is the config for reading, or the error that explains its absence.
func readConfig() (*config.Config, error) {
	if !keepsConfig {
		return nil, errorx.NoConfigHere()
	}
	path, err := configPath()
	if err != nil {
		return nil, errorx.Failed("locate the config", err)
	}
	cfg, err := config.Load(path)
	if err != nil {
		return nil, errorx.NoConfig(path, err)
	}
	return cfg, nil
}

type settingItem struct {
	Key   string `json:"key"`
	Value string `json:"value"`
	Help  string `json:"help"`
}

func runConfigLs(cmd *cobra.Command, _ []string) error {
	cfg, err := readConfig()
	if err != nil {
		return err
	}

	items := make([]settingItem, 0, len(config.Keys))
	rows := make([][]string, 0, len(config.Keys))
	for _, k := range config.Keys {
		item := settingItem{Key: k.Name, Value: k.Get(cfg), Help: k.Help}
		items = append(items, item)
		rows = append(rows, []string{item.Key, item.Value, item.Help})
	}
	return printList(cmd.OutOrStdout(), items, []string{"KEY", "VALUE", "MEANING"}, rows)
}

func runConfigGet(cmd *cobra.Command, args []string) error {
	key, err := config.Lookup(args[0])
	if err != nil {
		return errorx.BadSetting(err)
	}
	cfg, err := readConfig()
	if err != nil {
		return err
	}
	fmt.Fprintln(cmd.OutOrStdout(), key.Get(cfg))
	return nil
}

func runConfigSet(cmd *cobra.Command, args []string) error {
	if !keepsConfig {
		return errorx.NoConfigHere()
	}
	path, err := configPath()
	if err != nil {
		return errorx.Failed("locate the config", err)
	}
	if err := config.Set(path, args[0], args[1]); err != nil {
		return errorx.BadSetting(err)
	}

	// A running agent picks it up in place; one that is stopped reads the
	// file when it starts.
	switch err := call("reload", nil, nil); {
	case err == nil:
		say(cmd.OutOrStdout(), "%s set, and the agent is using it", args[0])
	case errors.Is(err, control.ErrNotRunning):
		say(cmd.OutOrStdout(), "%s set; the agent will use it when it starts", args[0])
	default:
		return err
	}
	return nil
}
