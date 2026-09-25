package cli

import (
	"fmt"
	"strings"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/ViitoJooj/synk/internal/agent"
	"github.com/ViitoJooj/synk/internal/config"
	"github.com/ViitoJooj/synk/internal/service"
	"github.com/ViitoJooj/synk/internal/transport"
)

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the agent, the peer and the settings in one screen",
	Long: `Show what synk is doing: whether the agent is running, which machine it
links with and whether that machine is reachable, and what is switched on.

This reports; it does not diagnose. When something is wrong, 'synk doctor'
says what and how to fix it.`,
	Args: cobra.NoArgs,
	RunE: runStatus,
}

func init() { rootCmd.AddCommand(statusCmd) }

type statusJSON struct {
	Services []service.State `json:"services"`
	Peer     *peerItem       `json:"peer"`
	// PeerError says why there is no peer, when there is none.
	PeerError string        `json:"peer_error,omitempty"`
	Settings  *settingsJSON `json:"settings,omitempty"`
}

type settingsJSON struct {
	Edge      string       `json:"edge"`
	Port      int          `json:"port"`
	Clipboard bool         `json:"clipboard"`
	Audio     config.Audio `json:"audio"`
}

func runStatus(cmd *cobra.Command, _ []string) error {
	var st statusJSON

	states, err := service.New().States()
	if err != nil {
		return err
	}
	st.Services = states

	cfg, cfgErr := loadConfig()
	if cfg != nil {
		st.Settings = &settingsJSON{
			Edge:      cfg.EdgeName,
			Port:      cfg.Port,
			Clipboard: cfg.Clipboard,
			Audio:     cfg.Audio,
		}
	}

	ts := transport.NewTailscale()
	if peer, err := agent.ResolvePeer(ts, chosenPeer(cfg), cfg); err != nil {
		st.PeerError = err.Error()
	} else {
		st.Peer = &peerItem{
			Name:     peer.Host,
			OS:       peer.OS,
			IP:       peer.IP,
			Online:   peer.Online,
			Direct:   peer.Direct,
			Linkable: peer.CanRunTheLink(),
			Current:  true,
		}
	}

	out := cmd.OutOrStdout()
	if asJSON {
		return printJSON(out, st)
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, s := range st.Services {
		fmt.Fprintf(w, "%s\t%s\t%s\n", s.Name, serviceWord(s), s.Unit)
	}

	if st.Peer != nil {
		p := st.Peer
		fmt.Fprintf(w, "peer\t%s\t%s, %s, %s\n", p.Name, p.OS, p.IP, lsStatus(*p)+", "+lsPath(*p))
	} else {
		fmt.Fprintf(w, "peer\tunknown\t%s\n", st.PeerError)
	}

	switch {
	case st.Settings != nil:
		s := st.Settings
		fmt.Fprintf(w, "edge\t%s\tthe peer's screen is beyond this one's %s edge\n", s.Edge, s.Edge)
		fmt.Fprintf(w, "clipboard\t%s\n", onOff(s.Clipboard))
		fmt.Fprintf(w, "audio\tplayback %s, mic %s\n", onOff(s.Audio.Playback), onOff(s.Audio.Mic))
	case cfgErr != nil:
		fmt.Fprintf(w, "config\tunreadable\t%v\n", cfgErr)
	}
	return w.Flush()
}

// serviceWord is the state as one phrase: "running", "stopped", "failed".
func serviceWord(s service.State) string {
	switch {
	case !s.Installed:
		return "not installed"
	case s.Running():
		return "running"
	case s.Active == "failed":
		return "failed"
	case s.Active == "inactive", s.Active == "Ready":
		return "stopped"
	default:
		return strings.ToLower(s.Active)
	}
}
