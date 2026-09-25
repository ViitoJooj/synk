package cli

import (
	"github.com/spf13/cobra"

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

type peerItem struct {
	Name     string `json:"name"`
	OS       string `json:"os"`
	IP       string `json:"ip"`
	Online   bool   `json:"online"`
	Direct   bool   `json:"direct"`
	Linkable bool   `json:"linkable"`
	Current  bool   `json:"current"`
}

func runLs(cmd *cobra.Command, _ []string) error {
	peers, err := transport.NewTailscale().Peers()
	if err != nil {
		return errorx.Tailscale(err)
	}

	// Without a config the current machine is simply unknown here; ls still
	// lists everything.
	cfg, _ := loadConfig()
	current := chosenPeer(cfg)

	items := make([]peerItem, 0, len(peers))
	rows := make([][]string, 0, len(peers))
	for _, p := range peers {
		item := peerItem{
			Name:     p.Host,
			OS:       p.OS,
			IP:       p.IP,
			Online:   p.Online,
			Direct:   p.Direct,
			Linkable: p.CanRunTheLink(),
			Current:  current != "" && sameMachine(p, current),
		}
		items = append(items, item)
		rows = append(rows, []string{mark(item.Current), item.Name, item.OS, item.IP, lsStatus(item), lsPath(item)})
	}

	return printList(cmd.OutOrStdout(), items, []string{"", "NAME", "OS", "ADDRESS", "STATUS", "PATH"}, rows)
}

func sameMachine(p transport.Peer, name string) bool {
	return transport.SameHost(p, name)
}

func mark(current bool) string {
	if current {
		return "*"
	}
	return ""
}

func lsStatus(p peerItem) string {
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
func lsPath(p peerItem) string {
	switch {
	case !p.Online:
		return "-"
	case p.Direct:
		return "direct"
	default:
		return "relayed"
	}
}
