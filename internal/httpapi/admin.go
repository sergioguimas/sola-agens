package httpapi

import (
	"crypto/sha256"
	"crypto/subtle"
	"net/http"
	"strings"

	"sola-agens/internal/mensagem"
	"sola-agens/internal/store"
)

// tokenValido compara o Bearer em tempo constante: compara os SHA-256, o que
// também esconde o tamanho do token. O token nunca vai para o log.
func (s *Server) tokenValido(r *http.Request) bool {
	h := r.Header.Get("Authorization")
	got, ok := strings.CutPrefix(h, "Bearer ")
	if !ok {
		got = ""
	}
	a := sha256.Sum256([]byte(got))
	b := sha256.Sum256([]byte(s.AdminToken))
	return subtle.ConstantTimeCompare(a[:], b[:]) == 1 && ok
}

// apagarContato implementa F10: apaga mensagens e conversa do contato e anonimiza
// os turnos. 204 também quando não havia nada (idempotente).
func (s *Server) apagarContato(w http.ResponseWriter, r *http.Request) {
	if !s.tokenValido(r) {
		s.log.Warn("admin rejeitado", "rota", "apagar_contato")
		http.Error(w, "não autorizado", http.StatusUnauthorized)
		return
	}
	id := store.ConversaID{Agente: r.PathValue("a"), Tenant: r.PathValue("t"), Contato: r.PathValue("c")}
	log := s.log.With("agente", id.Agente, "tenant", id.Tenant, "conversa", mensagem.HashContato(id.Contato))
	if _, ok := s.agentes[id.Agente]; !ok {
		log.Warn("agente desconhecido")
		http.Error(w, "agente desconhecido", http.StatusNotFound)
		return
	}
	if err := s.Apagador.ApagarContato(r.Context(), id); err != nil {
		log.Error("falha ao apagar contato", "erro", err.Error())
		http.Error(w, "erro interno", http.StatusInternalServerError)
		return
	}
	log.Info("contato apagado")
	w.WriteHeader(http.StatusNoContent)
}
