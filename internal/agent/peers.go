package agent

import "github.com/ViitoJooj/synk/internal/transport"

// PeerInfo is a machine on the tailnet as `synk ls` and the UI show it.
type PeerInfo struct {
	Name     string `json:"name"`
	OS       string `json:"os"`
	IP       string `json:"ip"`
	Online   bool   `json:"online"`
	Direct   bool   `json:"direct"`
	Linkable bool   `json:"linkable"`
	// Current marks the machine synk links with.
	Current bool `json:"current"`
}

func InfoOf(p transport.Peer, current string) PeerInfo {
	return PeerInfo{
		Name:     p.Host,
		OS:       p.OS,
		IP:       p.IP,
		Online:   p.Online,
		Direct:   p.Direct,
		Linkable: p.CanRunTheLink(),
		Current:  current != "" && transport.SameHost(p, current),
	}
}

// Peers lists the tailnet, marking current.
func Peers(ts *transport.Tailscale, current string) ([]PeerInfo, error) {
	peers, err := ts.Peers()
	if err != nil {
		return nil, err
	}
	out := make([]PeerInfo, len(peers))
	for i, p := range peers {
		out[i] = InfoOf(p, current)
	}
	return out, nil
}
