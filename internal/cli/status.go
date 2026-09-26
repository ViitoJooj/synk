package cli

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/ViitoJooj/synk/internal/agent"
	"github.com/ViitoJooj/synk/internal/config"
	"github.com/ViitoJooj/synk/internal/control"
	"github.com/ViitoJooj/synk/internal/service"
	"github.com/ViitoJooj/synk/internal/sync/engine"
	"github.com/ViitoJooj/synk/internal/transport"
)

var statusWatch bool

var statusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show the agent, the link and the settings in one screen",
	Long: `Show what synk is doing: whether the agent is running, whether the link
is up and which machine has the keyboard, and what is switched on.

With --watch it keeps printing a line every time the link changes, until
Ctrl+C. With --json each line is the agent's full status.

This reports; it does not diagnose. When something is wrong, 'synk doctor'
says what and how to fix it.`,
	Args: cobra.NoArgs,
	RunE: runStatus,
}

func init() {
	statusCmd.Flags().BoolVarP(&statusWatch, "watch", "w", false, "keep printing every change to the link")
	rootCmd.AddCommand(statusCmd)
}

type statusJSON struct {
	Services []service.State `json:"services"`
	// ServicesError says why the service manager could not be asked.
	ServicesError string `json:"services_error,omitempty"`
	// Agent is the running agent's own report, or nil when none answers.
	Agent *agent.Status   `json:"agent"`
	Peer  *agent.PeerInfo `json:"peer"`
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
	if statusWatch {
		return watchStatus(cmd)
	}

	var st statusJSON

	// A service manager that cannot be asked is one line of the report, not
	// a reason to show nothing: the agent may still answer below.
	states, err := service.New().States()
	if err != nil {
		st.ServicesError = err.Error()
	}
	st.Services = states

	var live agent.Status
	if err := call("status", nil, &live); err == nil {
		st.Agent = &live
	}

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
		info := agent.InfoOf(peer, peer.Host)
		st.Peer = &info
	}

	out := cmd.OutOrStdout()
	if asJSON {
		return printJSON(out, st)
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	for _, s := range st.Services {
		fmt.Fprintf(w, "%s\t%s\t%s\n", s.Name, serviceWord(s), s.Unit)
	}
	if st.ServicesError != "" {
		fmt.Fprintf(w, "services\tunknown\t%s\n", st.ServicesError)
	}

	if st.Agent != nil {
		fmt.Fprintf(w, "link\t%s\t%s\n", linkWord(st.Agent.Link), linkDetail(st.Agent.Link, time.Now()))
	} else if running(st.Services) {
		fmt.Fprintf(w, "link\tunknown\tthe agent is running but does not answer on %s\n", control.Address())
	}

	if st.Agent != nil && st.Agent.Sync != nil {
		fmt.Fprintf(w, "folders\t%s\t%s\n", foldersWord(st.Agent.Sync), "details in 'synk folder ls'")
	}

	if st.Peer != nil {
		p := *st.Peer
		fmt.Fprintf(w, "peer\t%s\t%s, %s, %s, %s\n", p.Name, p.OS, p.IP, lsStatus(p), lsPath(p))
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

// watchStatus prints a line per change until Ctrl+C.
func watchStatus(cmd *cobra.Command) error {
	ctx, stop := signalContext()
	defer stop()

	out := cmd.OutOrStdout()
	err := control.Watch(ctx, control.Address(), func(raw json.RawMessage) error {
		if asJSON {
			_, err := fmt.Fprintf(out, "%s\n", raw)
			return err
		}
		var st agent.Status
		if err := json.Unmarshal(raw, &st); err != nil {
			return err
		}
		return printWatchLine(out, st.Link, time.Now())
	})
	if errors.Is(err, control.ErrNotRunning) {
		return serviceError("watch", err)
	}
	return err
}

func printWatchLine(w io.Writer, l agent.Link, now time.Time) error {
	_, err := fmt.Fprintf(w, "%s  %-9s %s\n", now.Format("15:04:05"), linkWord(l), linkDetail(l, now))
	return err
}

func running(states []service.State) bool {
	for _, s := range states {
		if s.Name == "agent" {
			return s.Running()
		}
	}
	return false
}

func linkWord(l agent.Link) string {
	switch l.State {
	case agent.LinkConnected:
		return "up"
	case agent.LinkWaiting:
		return "waiting"
	default:
		return l.State
	}
}

// linkDetail is the rest of the link line: who, for how long, who has the
// keyboard, and whether sound is getting through.
func linkDetail(l agent.Link, now time.Time) string {
	switch l.State {
	case agent.LinkConnected:
		parts := []string{
			fmt.Sprintf("with %s for %s", l.Peer, since(l.Since, now)),
			"keyboard " + focusWord(l.Focus),
			"audio " + l.Audio,
		}
		return strings.Join(parts, ", ")
	case agent.LinkWaiting:
		detail := "for " + orAny(l.Peer)
		if l.LastError != "" {
			detail += "; last drop: " + l.LastError
		}
		return detail
	default:
		return "waiting for Tailscale"
	}
}

func focusWord(f string) string {
	switch f {
	case "local":
		return "here"
	case "peer":
		return "on the peer"
	}
	return f
}

func orAny(peer string) string {
	if peer == "" {
		return "the only computer online"
	}
	return peer
}

// since is a duration a person reads: "12m", "3h05m", "2d4h".
func since(t, now time.Time) string {
	d := now.Sub(t).Round(time.Second)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
	default:
		return fmt.Sprintf("%dd%dh", int(d.Hours())/24, int(d.Hours())%24)
	}
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

// foldersWord sums up the folders by phase: "2 in sync, 1 in preview".
func foldersWord(st *engine.Status) string {
	if len(st.Folders) == 0 {
		return "none"
	}
	count := map[string]int{}
	var order []string
	for _, f := range st.Folders {
		word := f.Phase
		if word == engine.PhaseIdle {
			word = "in sync"
		}
		if count[word] == 0 {
			order = append(order, word)
		}
		count[word]++
	}
	parts := make([]string, len(order))
	for i, w := range order {
		parts[i] = fmt.Sprintf("%d %s", count[w], w)
	}
	return strings.Join(parts, ", ")
}
