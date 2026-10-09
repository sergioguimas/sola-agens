package transcricao

import (
	"context"
	"fmt"
	"sync"
)

// Chamada registra os argumentos de uma chamada a Fake.Transcrever.
type Chamada struct {
	Audio    []byte
	Mimetype string
	Idioma   string
}

// Fake e um Transcritor roteirizado para testes sem rede (specs/05 §9):
// devolve Textos/Erros em sequencia (um por chamada, indice a indice) e
// grava cada chamada em Chamadas. Zero value ja funciona; thread-safe.
type Fake struct {
	mu sync.Mutex

	// Textos[i] e o texto devolvido na chamada i+1; Erros[i], se != nil,
	// tem prioridade sobre Textos[i] nessa mesma chamada.
	Textos   []string
	Erros    []error
	Usos     []Uso // opcional: Usos[i] acompanha a chamada i+1
	Chamadas []Chamada

	proximo int
}

var _ Transcritor = (*Fake)(nil)

func (f *Fake) Transcrever(_ context.Context, audio []byte, mimetype, idioma string) (string, Uso, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.Chamadas = append(f.Chamadas, Chamada{Audio: audio, Mimetype: mimetype, Idioma: idioma})

	i := f.proximo
	f.proximo++

	if i < len(f.Erros) && f.Erros[i] != nil {
		return "", Uso{}, f.Erros[i]
	}
	if i >= len(f.Textos) {
		return "", Uso{}, fmt.Errorf("transcricao.Fake: sem resposta roteirizada para a chamada %d", i+1)
	}
	var u Uso
	if i < len(f.Usos) {
		u = f.Usos[i]
	}
	return f.Textos[i], u, nil
}
