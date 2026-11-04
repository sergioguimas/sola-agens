package agentes

import (
	"strings"
	"testing"
)

const blocoTreino = `treino:
  segredo_env: TREINO
  header_assinatura: X-Sig
  header_timestamp: X-Ts
  papeis: [owner, admin]
`

func envTreino(segredo string) func(string) (string, bool) {
	base := env()
	return func(k string) (string, bool) {
		if k == "TREINO" {
			return segredo, segredo != ""
		}
		return base(k)
	}
}

func TestTreinoOpcional(t *testing.T) {
	dir := t.TempDir()
	gravar(t, dir, "eliza.yaml", yamlOK)
	ags, err := Carregar(dir, env())
	if err != nil {
		t.Fatal(err)
	}
	if ags[0].Treino != nil || ags[0].SegredoTreino() != "" {
		t.Errorf("sem o bloco o treino deveria ficar desligado: %+v", ags[0].Treino)
	}
}

func TestTreinoValido(t *testing.T) {
	dir := t.TempDir()
	gravar(t, dir, "eliza.yaml", yamlOK+blocoTreino)
	segredo := strings.Repeat("s", SegredoTreinoMin)
	ags, err := Carregar(dir, envTreino(segredo))
	if err != nil {
		t.Fatal(err)
	}
	a := ags[0]
	if a.SegredoTreino() != segredo {
		t.Error("segredo do treino não resolvido")
	}
	l := a.Treino.Limites
	if l.SessoesDiaTenant != 10 || l.MensagensSessao != 80 || l.TextoMax != 1000 || l.InatividadeMin != 30 || l.TurnoTimeoutS != 30 {
		t.Errorf("padrões: %+v", l)
	}
	if !a.Treino.PapelPermitido("owner") || a.Treino.PapelPermitido("staff") {
		t.Error("papéis permitidos errados")
	}
}

func TestTreinoInvalido(t *testing.T) {
	segredo := strings.Repeat("s", SegredoTreinoMin)
	casos := []struct {
		nome, bloco, segredo, quer string
	}{
		{"env ausente", blocoTreino, "", "não definida"},
		{"segredo curto", blocoTreino, "curto", "ao menos 32 bytes"},
		{"sem papéis", strings.Replace(blocoTreino, "  papeis: [owner, admin]\n", "", 1), segredo, "papeis"},
		{"sem header", strings.Replace(blocoTreino, "  header_timestamp: X-Ts\n", "", 1), segredo, "header_timestamp"},
		{"limite negativo", blocoTreino + "  limites:\n    texto_max: -1\n", segredo, "negativo"},
	}
	for _, c := range casos {
		t.Run(c.nome, func(t *testing.T) {
			dir := t.TempDir()
			gravar(t, dir, "eliza.yaml", yamlOK+c.bloco)
			_, err := Carregar(dir, envTreino(c.segredo))
			if err == nil || !strings.Contains(err.Error(), c.quer) {
				t.Fatalf("erro = %v, quer conter %q", err, c.quer)
			}
		})
	}
}
