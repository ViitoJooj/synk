package cli

import (
	"errors"
	"fmt"
	"io"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/ViitoJooj/synk/internal/agent"
	"github.com/ViitoJooj/synk/internal/config"
	"github.com/ViitoJooj/synk/internal/transport"
)

var doctorCmd = &cobra.Command{
	Use:   "doctor",
	Short: "Check everything the link depends on, and say how to fix what is wrong",
	Long: `Check every part the link depends on and report what is wrong.

Run this whenever something is not working. It answers the questions in the
order they matter: is the configuration readable, is Tailscale up, is the peer
online, is the privileged injector running, and can the two machines actually
reach each other.

It exits with status 1 when any check fails, so scripts can rely on it.`,
	Args: cobra.NoArgs,
	RunE: runDoctor,
}

func init() { rootCmd.AddCommand(doctorCmd) }

// check is one line of the report: what was inspected, what was found, and —
// when something is wrong — what to do about it.
type check struct {
	name   string
	ok     bool
	detail string
	fix    string
}

// checkJSON is a check as --json prints it.
type checkJSON struct {
	Name   string `json:"name"`
	OK     bool   `json:"ok"`
	Detail string `json:"detail"`
	Fix    string `json:"fix,omitempty"`
}

func runDoctor(cmd *cobra.Command, _ []string) error {
	cfg, cfgErr := loadConfig()

	checks := []check{checkConfig(cfg, cfgErr)}

	self, peer, tsChecks := checkTailscale(transport.NewTailscale(), cfg)
	checks = append(checks, tsChecks...)
	checks = append(checks, checkDaemon(cfg)...)
	checks = append(checks, checkPointer()...)
	checks = append(checks, checkLink(cfg, self, peer)...)
	checks = append(checks, checkSync(cfg, self, peer)...)

	out := cmd.OutOrStdout()
	if asJSON {
		items := make([]checkJSON, 0, len(checks))
		failed := false
		for _, c := range checks {
			items = append(items, checkJSON{c.name, c.ok, c.detail, c.fix})
			failed = failed || !c.ok
		}
		if err := printJSON(out, items); err != nil {
			return err
		}
		if failed {
			return errReported
		}
		return nil
	}

	w := tabwriter.NewWriter(out, 0, 0, 2, ' ', 0)
	failed := 0
	for _, c := range checks {
		mark := "ok"
		if !c.ok {
			mark = "--"
			failed++
		}
		fmt.Fprintf(w, "  %s\t%s\t%s\n", mark, c.name, c.detail)
	}
	w.Flush()

	if failed == 0 {
		fmt.Fprintln(out, "\nEverything the link needs is in place.")
		return nil
	}

	fmt.Fprintln(out, "\nTo fix:")
	for _, c := range checks {
		if !c.ok && c.fix != "" {
			fmt.Fprintf(out, "  %s: %s\n", c.name, c.fix)
		}
	}
	return errReported
}

// checkConfig reports the settings file. A nil config with no error is the
// sending machine, which reads none by design.
func checkConfig(cfg *config.Config, err error) check {
	path, pathErr := configPath()
	if pathErr != nil {
		return check{name: "config", detail: pathErr.Error()}
	}

	switch {
	case err == nil && cfg == nil:
		return check{
			name:   "config",
			ok:     true,
			detail: "none needed on this machine; the peer sends what is required",
		}

	case err == nil:
		return check{
			name: "config",
			ok:   true,
			detail: fmt.Sprintf("peer %s, port %d, peer screen on the %s",
				cfg.Peer, cfg.Port, cfg.EdgeName),
		}

	case errors.Is(err, os.ErrNotExist):
		if old := config.Legacy(); old != "" {
			return check{
				name:   "config",
				detail: "not found at " + path + ", but the old one is at " + old,
				fix:    "run hacks/install.sh, which moves it and the rest of the old install over",
			}
		}
		return check{
			name:   "config",
			detail: "not found at " + path,
			fix:    "write a config.yaml there naming the peer and the edge its screen sits beyond; after that, 'synk <peer>' and 'synk config set' change it",
		}

	default:
		return check{name: "config", detail: err.Error(), fix: "correct " + path}
	}
}

// checkTailscale reports this machine's address and the peer's, and returns
// both so the platform check can go on to test the path between them.
func checkTailscale(ts *transport.Tailscale, cfg *config.Config) (string, *transport.Peer, []check) {
	self, err := ts.Self()
	if err != nil {
		return "", nil, []check{{
			name:   "tailscale",
			detail: err.Error(),
			fix:    "run `sudo tailscale up` and authenticate in the browser",
		}}
	}
	out := []check{{name: "tailscale", ok: true, detail: "this machine is " + self}}

	peer, err := resolvePeer(ts, cfg)
	if err != nil {
		return self, nil, append(out, check{
			name:   "peer",
			detail: err.Error(),
			fix:    "check the peer name against `tailscale status`, and that the other machine is awake",
		})
	}
	if !peer.Online {
		return self, nil, append(out, check{
			name:   "peer",
			detail: fmt.Sprintf("%s (%s) is offline", peer.Host, peer.IP),
			fix:    "start the other machine, or check Tailscale is running on it",
		})
	}

	path := "relayed through a DERP server"
	if peer.Direct {
		path = "direct peer-to-peer"
	} else {
		// Worth saying plainly: a peer that has exchanged no traffic yet always
		// looks relayed, and people read that as a fault.
		path += " (normal until the link has carried traffic; check again once connected)"
	}
	return self, &peer, append(out, check{
		name:   "peer",
		ok:     true,
		detail: fmt.Sprintf("%s (%s) online, %s", peer.Host, peer.IP, path),
	})
}

// resolvePeer names the other machine the way the agent will.
func resolvePeer(ts *transport.Tailscale, cfg *config.Config) (transport.Peer, error) {
	return agent.ResolvePeer(ts, chosenPeer(cfg), cfg)
}

func closeQuietly(c io.Closer) { _ = c.Close() }
