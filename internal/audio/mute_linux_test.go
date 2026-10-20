//go:build linux

package audio

import (
	"errors"
	"strings"
	"testing"
)

// fakePactl grava os comandos e responde o que o teste mandar.
type fakePactl struct {
	calls    [][]string
	getSaida string
	getErr   error
	setErr   error
}

func (f *fakePactl) run(args ...string) ([]byte, error) {
	f.calls = append(f.calls, args)
	if len(args) > 0 && args[0] == "get-sink-mute" {
		return []byte(f.getSaida), f.getErr
	}
	return nil, f.setErr
}

func (f *fakePactl) chamada(i int) string { return strings.Join(f.calls[i], " ") }

func TestSilenceGuardaOEstadoEMuta(t *testing.T) {
	p := &fakePactl{getSaida: "Mute: no\n"}
	m := &sinkMute{run: p.run, sink: "sink0"}

	if err := m.silence(); err != nil {
		t.Fatalf("silence: %v", err)
	}
	if len(p.calls) != 2 {
		t.Fatalf("rodou %d comandos, quero 2: %v", len(p.calls), p.calls)
	}
	if got := p.chamada(0); got != "get-sink-mute sink0" {
		t.Errorf("primeiro comando = %q", got)
	}
	if got := p.chamada(1); got != "set-sink-mute sink0 1" {
		t.Errorf("segundo comando = %q", got)
	}
}

func TestRestoreDevolveOEstadoQueEncontrou(t *testing.T) {
	// O usuário já estava com o som mudo antes do link. Restaurar tem que
	// deixar mudo, não "desmutar por padrão".
	p := &fakePactl{getSaida: "Mute: yes\n"}
	m := &sinkMute{run: p.run, sink: "sink0"}

	if err := m.silence(); err != nil {
		t.Fatalf("silence: %v", err)
	}
	if err := m.restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if got := p.chamada(len(p.calls) - 1); got != "set-sink-mute sink0 1" {
		t.Errorf("último comando = %q, quero devolver o mudo", got)
	}
}

func TestRestoreDesmutaQuandoNaoEstavaMudo(t *testing.T) {
	p := &fakePactl{getSaida: "Mute: no\n"}
	m := &sinkMute{run: p.run, sink: "sink0"}
	_ = m.silence()
	_ = m.restore()
	if got := p.chamada(len(p.calls) - 1); got != "set-sink-mute sink0 0" {
		t.Errorf("último comando = %q, quero desmutar", got)
	}
}

func TestRestoreSemSilenceNaoFazNada(t *testing.T) {
	// Se silence falhou, restore não pode inventar um estado e escrevê-lo.
	p := &fakePactl{}
	m := &sinkMute{run: p.run, sink: "sink0"}
	if err := m.restore(); err != nil {
		t.Fatalf("restore: %v", err)
	}
	if len(p.calls) != 0 {
		t.Errorf("rodou %v, não devia rodar nada", p.calls)
	}
}

func TestSilenceNaoMutaSeNaoConseguiuLerOEstado(t *testing.T) {
	// Mutar sem saber o estado anterior é como o usuário fica mudo para
	// sempre: não haveria o que restaurar.
	p := &fakePactl{getErr: errors.New("pactl sumiu")}
	m := &sinkMute{run: p.run, sink: "sink0"}

	if err := m.silence(); err == nil {
		t.Fatal("silence devia falhar quando não consegue ler o estado")
	}
	for _, c := range p.calls {
		if c[0] == "set-sink-mute" {
			t.Errorf("mutou mesmo sem saber o estado anterior: %v", c)
		}
	}
}
