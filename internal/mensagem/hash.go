package mensagem

import (
	"crypto/sha256"
	"encoding/hex"
)

// HashContato é sha256(contato)[:12] em hex: o único identificador do contato
// permitido em log (specs/05 §8).
func HashContato(contato string) string {
	return HashCompleto(contato)[:12]
}

// HashCompleto é sha256(contato) em hex inteiro: o que substitui o contato nos
// turnos depois do apagamento (specs/01 §9). Não vai para log.
func HashCompleto(contato string) string {
	h := sha256.Sum256([]byte(contato))
	return hex.EncodeToString(h[:])
}
