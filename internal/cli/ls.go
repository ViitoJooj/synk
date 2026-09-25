package cli

import (
	"github.com/spf13/cobra"

	"github.com/ViitoJooj/synk/internal/agent"
	"github.com/ViitoJooj/synk/internal/transport"
	"github.com/ViitoJooj/synk/pkg/errorx"
)

var lsCmd = &cobra.Command{
	Use:     "ls",
	Aliases: []string{"list", "peers"},
	Short:   "List the machines on your tailnet, and which one synk links with",
	Long: `List the machines on your tailnet.

The one marked * is the one synk links with. Machines that cannot run synk,
such as phones, are listed but marked: they have no keyboard and mouse to
share.`,
	Args: cobra.NoArgs,
	RunE: runLs,
}

func init() { rootCmd.AddCommand(lsCmd) }

func runLs(cmd *cobra.Command, _ []string) error {
	// Without a config the current machine is simply unknown here; ls still
	// lists everything.
	cfg, _ := loadConfig()
	peers, err := agent.Peers(transport.NewTailscale(), chosenPeer(cfg))
	if err != nil {
		return errorx.Tailscale(err)
	}

	rows := make([][]string, 0, len(peers))
	for _, p := range peers {
		rows = append(rows, []string{mark(p.Current), p.Name, p.OS, p.IP, lsStatus(p), lsPath(p)})
	}
	return printList(cmd.OutOrStdout(), peers, []string{"", "NAME", "OS", "ADDRESS", "STATUS", "PATH"}, rows)
}

func mark(current bool) string {
	if current {
		return "*"
	}
	return ""
}

func lsStatus(p agent.PeerInfo) string {
	switch {
	case !p.Linkable && p.Online:
		return "online, no synk"
	case !p.Linkable:
		return "offline, no synk"
	case p.Online:
		return "online"
	default:
		return "offline"
	}
}

// lsPath says how traffic gets there. A relayed path works but adds latency
// the pointer will show, and a peer that has carried no traffic yet always
// looks relayed.
func lsPath(p agent.PeerInfo) string {
	switch {
	case !p.Online:
		return "-"
	case p.Direct:
		return "direct"
	default:
		return "relayed"
	}
}
