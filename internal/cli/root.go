// Package cli is synk's command line: it parses flags, finds the config and
// hands the work to the agent, the injector and the service manager.
//
// Commands are nouns and verbs in the style of gh and docker (`synk ls`,
// `synk up`), with one exception borrowed from the shell: `synk <peer>` on its
// own switches to that machine, the way `cd` needs no verb.
//
// synk began as a subcommand of the wfkit CLI, as wfkit-link and then conn, and
// left to be its own project: it is a two-machine app with its own life cycle,
// and it has to cross-compile to Windows, which the wfkit CLI cannot.
package cli

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"

	"github.com/ViitoJooj/synk/internal/config"
	"github.com/ViitoJooj/synk/pkg/errorx"
)

var (
	// linkPeer and linkPort override discovery and the default port. They
	// exist for the machine that carries no configuration file, and as an
	// escape hatch when a tailnet has more than one candidate on it.
	linkPeer string
	linkPort int

	asJSON bool
	quiet  bool
)

var rootCmd = &cobra.Command{
	Use:   "synk [peer]",
	Short: "Share keyboard, mouse, clipboard and audio with a machine on your tailnet",
	Long: `Share keyboard, mouse, clipboard and audio with a machine on your tailnet.

synk <peer> switches to that machine and restarts the agent. The peer is a
name from 'synk ls', or an operating system when exactly one machine running it
is online:

  synk windows          the only Windows machine online
  synk desktop-6v4goh8  that machine, by name`,
	Example: `  synk ls
  synk windows
  synk status --json
  synk logs -f`,
	Args: cobra.MaximumNArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) == 0 {
			return cmd.Help()
		}
		return runConnect(cmd, args[0])
	},

	SilenceUsage:  true,
	SilenceErrors: true,

	// With `synk <peer>` at the root, a mistyped command reaches the peer
	// lookup; this is how close it has to be to get a "did you mean".
	SuggestionsMinimumDistance: 2,
}

func init() {
	rootCmd.PersistentPreRunE = startOnDemand

	flags := rootCmd.PersistentFlags()
	flags.StringVar(&linkPeer, "peer", "",
		"tailnet name of the other machine (default: the one chosen with 'synk <peer>', or the only computer online)")
	flags.IntVar(&linkPort, "port", 0, "port to use instead of the configured one")
	flags.BoolVar(&asJSON, "json", false, "print JSON instead of a table, for scripts")
	flags.BoolVarP(&quiet, "quiet", "q", false, "print only errors")
}

// errReported is returned by a command that has already said what is wrong,
// and only needs the exit status to reflect it.
var errReported = errors.New("reported")

// Execute runs the command line and exits non-zero on failure.
func Execute() {
	err := rootCmd.Execute()
	if err == nil {
		return
	}
	if !errors.Is(err, errReported) {
		errorx.Print(os.Stderr, err)
	}
	os.Exit(1)
}

// signalContext is cancelled by Ctrl+C or by systemd stopping the unit.
func signalContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}

func configDir() (string, error)  { return config.Dir() }
func configPath() (string, error) { return config.Path() }
