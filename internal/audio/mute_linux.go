//go:build linux

package audio

import (
	"fmt"
	"os/exec"
	"strings"
)

// sinkMute silencia as caixas desta máquina enquanto o áudio está sendo
// transmitido para o peer, e devolve o estado anterior no fim.
//
// Por que mutar e não trocar o sink padrão por um nulo: o monitor de um sink
// MUDO continua carregando o áudio inteiro. Medido nesta máquina — tocando um
// tom de 440 Hz, o pico no monitor foi 20000 com o sink normal e 20000 com ele
// mudo. Então mutar silencia as caixas sem afetar em nada o que é capturado e
// enviado.
//
// A alternativa (module-null-sink como padrão) funcionaria igual, mas o modo de
// falha é bem pior: se o processo morrer sem restaurar, o usuário fica sem
// saída de áudio nenhuma e sem pista do motivo. Ficar mudo é óbvio e a tecla de
// volume resolve.
type sinkMute struct {
	run  func(args ...string) ([]byte, error)
	sink string

	anterior string // "0" ou "1", como o pactl aceita
	guardado bool
}

func newSinkMute(sink string) *sinkMute {
	return &sinkMute{
		run: func(args ...string) ([]byte, error) {
			return exec.Command("pactl", args...).Output()
		},
		sink: sink,
	}
}

// silence guarda o estado atual e muta.
//
// Falha sem mutar quando não consegue ler o estado: mutar às cegas é como o
// usuário fica mudo para sempre, porque não haveria o que restaurar.
func (s *sinkMute) silence() error {
	out, err := s.run("get-sink-mute", s.sink)
	if err != nil {
		return fmt.Errorf("audio: lendo o mute de %s: %w", s.sink, err)
	}

	s.anterior = "0"
	if strings.Contains(strings.ToLower(string(out)), "yes") {
		s.anterior = "1"
	}
	s.guardado = true

	if _, err := s.run("set-sink-mute", s.sink, "1"); err != nil {
		s.guardado = false
		return fmt.Errorf("audio: mutando %s: %w", s.sink, err)
	}
	return nil
}

// restore devolve o estado que silence encontrou. Sem silence antes, não faz
// nada: inventar um estado e escrevê-lo desmutaria quem já estava mudo.
func (s *sinkMute) restore() error {
	if !s.guardado {
		return nil
	}
	s.guardado = false
	if _, err := s.run("set-sink-mute", s.sink, s.anterior); err != nil {
		return fmt.Errorf("audio: restaurando o mute de %s: %w", s.sink, err)
	}
	return nil
}
