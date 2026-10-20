package transport

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"
)

// scriptedRunner devolve uma saída diferente a cada chamada, para simular o
// tailscale subindo: NeedsLogin, depois Starting, depois Running.
type scriptedRunner struct {
	outs  []string
	calls int
}

func (s *scriptedRunner) Run(string, ...string) ([]byte, error) {
	i := s.calls
	if i >= len(s.outs) {
		i = len(s.outs) - 1
	}
	s.calls++
	return []byte(s.outs[i]), nil
}

const needsLogin = `{"BackendState": "NeedsLogin"}`
const starting = `{"BackendState": "Starting"}`

// peerOffline é o runningStatus com o desktop fora do ar.
const peerOffline = `{
  "BackendState": "Running",
  "Self": {
    "HostName": "laptop",
    "DNSName": "laptop.tail0000.ts.net.",
    "TailscaleIPs": ["100.101.102.103"],
    "Online": true
  },
  "Peer": {
    "nodekey:aaa": {
      "HostName": "DESKTOP-PC",
      "DNSName": "desktop-pc.tail0000.ts.net.",
      "TailscaleIPs": ["100.64.10.20"],
      "Online": false
    }
  }
}`

// recordingSleeper não dorme: só anota quanto teria dormido.
type recordingSleeper struct{ waits []time.Duration }

func (r *recordingSleeper) sleep(ctx context.Context, d time.Duration) error {
	r.waits = append(r.waits, d)
	return ctx.Err()
}

func TestWaitLocalEsperaOTailscaleSubir(t *testing.T) {
	run := &scriptedRunner{outs: []string{needsLogin, starting, runningStatus}}
	sleeper := &recordingSleeper{}
	ts := NewTailscaleWithRunner(run)
	ts.sleep = sleeper.sleep

	var linhas []string
	ip, err := ts.WaitLocal(context.Background(), func(m string) { linhas = append(linhas, m) })
	if err != nil {
		t.Fatalf("WaitLocal: %v", err)
	}
	if ip != "100.101.102.103" {
		t.Errorf("ip = %q, quero 100.101.102.103", ip)
	}
	if run.calls != 3 {
		t.Errorf("consultou o tailscale %d vezes, quero 3", run.calls)
	}
	if len(sleeper.waits) != 2 {
		t.Fatalf("dormiu %d vezes, quero 2", len(sleeper.waits))
	}
	// Recuo progressivo: a segunda espera é maior que a primeira.
	if sleeper.waits[1] <= sleeper.waits[0] {
		t.Errorf("sem recuo: %v depois %v", sleeper.waits[0], sleeper.waits[1])
	}
}

func TestWaitLocalNaoRepeteAMesmaLinha(t *testing.T) {
	// Cinco consultas com o MESMO motivo devem produzir UMA linha de log.
	// É isso que evita o journal encher com a mesma frase a cada 5 segundos.
	run := &scriptedRunner{outs: []string{needsLogin, needsLogin, needsLogin, needsLogin, runningStatus}}
	ts := NewTailscaleWithRunner(run)
	ts.sleep = (&recordingSleeper{}).sleep

	var linhas []string
	if _, err := ts.WaitLocal(context.Background(), func(m string) { linhas = append(linhas, m) }); err != nil {
		t.Fatalf("WaitLocal: %v", err)
	}
	if len(linhas) != 1 {
		t.Fatalf("logou %d linhas, quero 1: %v", len(linhas), linhas)
	}
	if !strings.Contains(linhas[0], "not logged in") {
		t.Errorf("linha = %q, quero mencionar o motivo", linhas[0])
	}
}

func TestWaitLocalRespeitaOCancelamento(t *testing.T) {
	run := &scriptedRunner{outs: []string{needsLogin}}
	ts := NewTailscaleWithRunner(run)
	ts.sleep = (&recordingSleeper{}).sleep

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := ts.WaitLocal(ctx, func(string) {}); !errors.Is(err, context.Canceled) {
		t.Errorf("err = %v, quero context.Canceled", err)
	}
}

func TestWaitPeerEsperaOPeerFicarOnline(t *testing.T) {
	run := &scriptedRunner{outs: []string{peerOffline, peerOffline, runningStatus}}
	ts := NewTailscaleWithRunner(run)
	ts.sleep = (&recordingSleeper{}).sleep

	var linhas []string
	p, err := ts.WaitPeer(context.Background(), "DESKTOP-PC", func(m string) { linhas = append(linhas, m) })
	if err != nil {
		t.Fatalf("WaitPeer: %v", err)
	}
	if !p.Online {
		t.Error("peer voltou offline")
	}
	if p.IP != "100.64.10.20" {
		t.Errorf("ip = %q, quero 100.64.10.20", p.IP)
	}
	if len(linhas) != 1 {
		t.Errorf("logou %d linhas, quero 1: %v", len(linhas), linhas)
	}
}
