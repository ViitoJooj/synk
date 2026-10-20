package tailscale

import (
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
)

var ErrUnavailable = errors.New("tailscale unavailable")

func wrapUnavailable(err error) error {
	return fmt.Errorf("%w: %v", ErrUnavailable, err)
}

type Peer struct {
	NodeID string
	Name   string
	IP     string
	Online bool
}

var cgnat = mustCIDR("100.64.0.0/10")

func mustCIDR(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

type tsPeer struct {
	ID           string   `json:"ID"`
	HostName     string   `json:"HostName"`
	TailscaleIPs []string `json:"TailscaleIPs"`
	Online       bool     `json:"Online"`
}

type tsStatus struct {
	Peer map[string]*tsPeer `json:"Peer"`
}

func Peers() ([]Peer, error) {
	bin, err := binaryPath()
	if err != nil {
		return nil, err
	}

	out, err := exec.Command(bin, "status", "--json").Output()
	if err != nil {
		return nil, wrapUnavailable(err)
	}

	var st tsStatus
	if err := json.Unmarshal(out, &st); err != nil {
		return nil, fmt.Errorf("%w: parsing status: %v", ErrUnavailable, err)
	}

	var peers []Peer
	for _, p := range st.Peer {
		if p == nil || !p.Online {
			continue
		}
		ip, ok := firstCGNATIPv4(p.TailscaleIPs)
		if !ok {
			continue
		}
		peers = append(peers, Peer{NodeID: p.ID, Name: p.HostName, IP: ip, Online: true})
	}
	return peers, nil
}

func SelfIP() (string, error) {
	bin, err := binaryPath()
	if err != nil {
		return "", err
	}
	out, err := exec.Command(bin, "ip", "-4").Output()
	if err != nil {
		return "", wrapUnavailable(err)
	}
	ip := strings.TrimSpace(string(out))
	if ip == "" {
		return "", fmt.Errorf("%w: empty response from \"tailscale ip -4\"", ErrUnavailable)
	}
	return ip, nil
}

func firstCGNATIPv4(ips []string) (string, bool) {
	for _, s := range ips {
		ip := net.ParseIP(s)
		if ip == nil {
			continue
		}
		ip4 := ip.To4()
		if ip4 == nil || !cgnat.Contains(ip4) {
			continue
		}
		return ip4.String(), true
	}
	return "", false
}

func binaryPath() (string, error) {
	if p, err := exec.LookPath("tailscale"); err == nil {
		return p, nil
	}

	var candidates []string
	if runtime.GOOS == "windows" {
		for _, envVar := range []string{"ProgramFiles", "ProgramFiles(x86)"} {
			if dir := os.Getenv(envVar); dir != "" {
				candidates = append(candidates, filepath.Join(dir, "Tailscale", "tailscale.exe"))
			}
		}
	} else {
		candidates = append(candidates, "/usr/bin/tailscale", "/usr/local/bin/tailscale")
	}

	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			return c, nil
		}
	}
	return "", fmt.Errorf("%w: tailscale binary not found — install it and log in", ErrUnavailable)
}
