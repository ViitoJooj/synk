package transport

import (
	"context"
	"fmt"
	"time"
)

// Esperar em vez de morrer.
//
// Antes, a sessão saía com erro quando o tailscale não estava pronto, e o
// systemd a reiniciava a cada 5 segundos — medido: 38 inicializações em 7 dias,
// com rajadas enquanto o backend subia. Cada uma acorda a CPU e escreve no
// journal. Num laptop isso é bateria gasta para descobrir que nada mudou.
const (
	waitFirst = 2 * time.Second
	waitMax   = 30 * time.Second
)

func realSleep(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-t.C:
		return nil
	}
}

// waitFor consulta try até ele dar certo, dobrando a espera de waitFirst até
// waitMax.
//
// report só é chamado quando o MOTIVO muda. Um PC desligado a noite inteira
// produz uma linha, não mil — e quando o motivo muda ("não logado" para
// "subindo") isso aparece, que é justamente a informação útil.
func (t *Tailscale) waitFor(ctx context.Context, report func(string), try func() error) error {
	espera := waitFirst
	ultimo := ""

	for {
		if err := ctx.Err(); err != nil {
			return err
		}

		err := try()
		if err == nil {
			return nil
		}

		if msg := err.Error(); msg != ultimo {
			ultimo = msg
			if report != nil {
				report(msg)
			}
		}

		dormir := t.sleep
		if dormir == nil {
			dormir = realSleep
		}
		if err := dormir(ctx, espera); err != nil {
			return err
		}

		espera *= 2
		if espera > waitMax {
			espera = waitMax
		}
	}
}

// WaitLocal bloqueia até o tailscale desta máquina estar em Running e devolve
// o IPv4 do tailnet.
//
// A escuta só pode ser aberta depois disso: o socket é ligado ao endereço do
// tailnet, e sem ele o certo é esperar, não cair para a rede local — expor
// injeção de teclado ao Wi-Fi do café seria bem pior que ficar sem link.
func (t *Tailscale) WaitLocal(ctx context.Context, report func(string)) (string, error) {
	var ip string
	err := t.waitFor(ctx, report, func() error {
		var err error
		ip, err = t.Self()
		return err
	})
	if err != nil {
		return "", err
	}
	return ip, nil
}

// WaitPeer bloqueia até o peer nomeado existir no tailnet E estar online.
//
// Estar no tailnet não basta: um PC desligado continua listado, só que com
// Online falso. É essa diferença que evita discar — ou anunciar link — contra
// uma máquina que não vai responder.
func (t *Tailscale) WaitPeer(ctx context.Context, name string, report func(string)) (Peer, error) {
	var p Peer
	err := t.waitFor(ctx, report, func() error {
		var err error
		p, err = t.Peer(name)
		if err != nil {
			return err
		}
		if !p.Online {
			return fmt.Errorf("transport: peer %q is offline", p.Host)
		}
		return nil
	})
	if err != nil {
		return Peer{}, err
	}
	return p, nil
}
