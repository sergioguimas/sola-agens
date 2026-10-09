// Package storetest é a bateria de contrato de store.Store: qualquer
// implementação (sqlite hoje, postgres no futuro) chama Rodar (specs/05 §5).
package storetest

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	"sola-agens/internal/mensagem"
	"sola-agens/internal/store"
)

var (
	passado = time.Unix(0, 0)
	futuro  = time.Now().AddDate(100, 0, 0)
	base    = time.Date(2026, 10, 9, 12, 0, 0, 0, time.UTC)
)

func ptr(s string) *string { return &s }

func texto(m store.MensagemMemoria) string {
	if m.Conteudo == nil {
		return "<nil>"
	}
	return *m.Conteudo
}

func textos(ms []store.MensagemMemoria) []string {
	out := make([]string, len(ms))
	for i, m := range ms {
		out[i] = texto(m)
	}
	return out
}

func igual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func cli(t *testing.T, s store.Store, id store.ConversaID, conteudo string, min int) {
	t.Helper()
	err := s.AnexarMensagem(context.Background(), id, store.MensagemMemoria{
		Papel: store.PapelCliente, Conteudo: ptr(conteudo), CriadaEm: base.Add(time.Duration(min) * time.Minute),
	})
	if err != nil {
		t.Fatalf("AnexarMensagem(%s): %v", conteudo, err)
	}
}

// Rodar executa toda a bateria; nova devolve um Store limpo a cada chamada.
func Rodar(t *testing.T, nova func(t *testing.T) store.Store) {
	ctx := context.Background()
	id := store.ConversaID{Agente: "eliza", Tenant: "org-1", Contato: "5511999990001"}

	t.Run("evento novo, duplicado e desmarcado", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		novo, err := s.MarcarEvento(ctx, "eliza", "M1")
		if err != nil || !novo {
			t.Fatalf("primeira vez: novo=%v err=%v", novo, err)
		}
		novo, err = s.MarcarEvento(ctx, "eliza", "M1")
		if err != nil || novo {
			t.Fatalf("duplicado: novo=%v err=%v", novo, err)
		}
		// outro agente com o mesmo id não é duplicado
		if novo, _ := s.MarcarEvento(ctx, "site", "M1"); !novo {
			t.Error("mesmo mensagemId em outro agente deveria ser novo")
		}
		if err := s.DesmarcarEvento(ctx, "eliza", "M1"); err != nil {
			t.Fatal(err)
		}
		if novo, _ := s.MarcarEvento(ctx, "eliza", "M1"); !novo {
			t.Error("depois de desmarcar deveria ser novo")
		}
		// desmarcar o que não existe não falha
		if err := s.DesmarcarEvento(ctx, "eliza", "nunca"); err != nil {
			t.Errorf("desmarcar inexistente: %v", err)
		}
	})

	t.Run("eco", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		if eco, err := s.EhEco(ctx, "eliza", "R1"); err != nil || eco {
			t.Fatalf("antes de registrar: eco=%v err=%v", eco, err)
		}
		if err := s.RegistrarEco(ctx, "eliza", "R1"); err != nil {
			t.Fatal(err)
		}
		if err := s.RegistrarEco(ctx, "eliza", "R1"); err != nil {
			t.Fatalf("registrar duas vezes: %v", err)
		}
		if eco, _ := s.EhEco(ctx, "eliza", "R1"); !eco {
			t.Error("deveria ser eco")
		}
		if eco, _ := s.EhEco(ctx, "site", "R1"); eco {
			t.Error("eco não atravessa agente")
		}
	})

	t.Run("conversa criada vazia, pausa e config", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		c, err := s.Conversa(ctx, id)
		if err != nil {
			t.Fatal(err)
		}
		if c.ID != id || c.PausadaAte != nil || c.ConfigJSON != nil || c.ConfigEm != nil || c.CriadaEm.IsZero() {
			t.Fatalf("conversa nova inesperada: %+v", c)
		}
		ate := base.Add(12 * time.Hour)
		if err := s.Pausar(ctx, id, ate); err != nil {
			t.Fatal(err)
		}
		if err := s.GuardarConfig(ctx, id, []byte(`{"persona":"Eliza"}`)); err != nil {
			t.Fatal(err)
		}
		c, _ = s.Conversa(ctx, id)
		if c.PausadaAte == nil || !c.PausadaAte.Equal(ate) {
			t.Errorf("pausada_ate = %v, quer %v", c.PausadaAte, ate)
		}
		if string(c.ConfigJSON) != `{"persona":"Eliza"}` || c.ConfigEm == nil {
			t.Errorf("config = %q em %v", c.ConfigJSON, c.ConfigEm)
		}
		// nova deMim reinicia o prazo
		novo := ate.Add(time.Hour)
		_ = s.Pausar(ctx, id, novo)
		c, _ = s.Conversa(ctx, id)
		if !c.PausadaAte.Equal(novo) {
			t.Errorf("pausa não reiniciou: %v", c.PausadaAte)
		}
		// Pausar e GuardarConfig criam a conversa se ela não existe
		outro := store.ConversaID{Agente: "eliza", Tenant: "org-1", Contato: "5511999990002"}
		if err := s.GuardarConfig(ctx, outro, []byte(`{}`)); err != nil {
			t.Fatal(err)
		}
		if c, _ := s.Conversa(ctx, outro); string(c.ConfigJSON) != `{}` {
			t.Errorf("config da conversa criada por GuardarConfig = %q", c.ConfigJSON)
		}
	})

	t.Run("chaves vazias", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		ruim := store.ConversaID{Agente: "eliza", Tenant: "", Contato: "x"}
		if _, err := s.Conversa(ctx, ruim); !errors.Is(err, store.ErrConversaInvalida) {
			t.Errorf("Conversa: %v", err)
		}
		if err := s.Pausar(ctx, ruim, base); !errors.Is(err, store.ErrConversaInvalida) {
			t.Errorf("Pausar: %v", err)
		}
		if err := s.AnexarMensagem(ctx, ruim, store.MensagemMemoria{Papel: store.PapelCliente}); !errors.Is(err, store.ErrConversaInvalida) {
			t.Errorf("AnexarMensagem: %v", err)
		}
		if err := s.AnexarMensagem(ctx, id, store.MensagemMemoria{Papel: "robo"}); !errors.Is(err, store.ErrPapelInvalido) {
			t.Errorf("papel inválido: %v", err)
		}
	})

	t.Run("janela: limite, desde e ordem", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		// gravadas fora de ordem de propósito
		cli(t, s, id, "m3", 3)
		cli(t, s, id, "m1", 1)
		cli(t, s, id, "m5", 5)
		cli(t, s, id, "m2", 2)
		cli(t, s, id, "m4", 4)

		casos := []struct {
			nome  string
			max   int
			desde time.Time
			quer  []string
		}{
			{"todas", 10, passado, []string{"m1", "m2", "m3", "m4", "m5"}},
			{"limite pega as últimas", 3, passado, []string{"m3", "m4", "m5"}},
			{"desde corta as antigas", 10, base.Add(3 * time.Minute), []string{"m3", "m4", "m5"}},
			{"desde e limite", 2, base.Add(2 * time.Minute), []string{"m4", "m5"}},
			{"desde no futuro", 10, base.Add(time.Hour), []string{}},
			{"max zero", 0, passado, []string{}},
		}
		for _, c := range casos {
			got, err := s.Janela(ctx, id, c.max, c.desde)
			if err != nil {
				t.Fatalf("%s: %v", c.nome, err)
			}
			if !igual(textos(got), c.quer) {
				t.Errorf("%s: %v, quer %v", c.nome, textos(got), c.quer)
			}
		}
	})

	t.Run("papéis, anexos sem Ref e conteúdo vazio", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		anexos := []mensagem.Anexo{
			{Tipo: "audio", Mime: "audio/ogg", Tamanho: 1234, DuracaoS: 12, Encaminhado: true, Ref: "SEGREDO-REF"},
			{Tipo: "outro", TipoOriginal: "sticker", Ref: "R2"},
		}
		for i, m := range []store.MensagemMemoria{
			{Papel: store.PapelCliente, Conteudo: ptr("[áudio 12s] oi"), Anexos: anexos},
			{Papel: store.PapelAgente, Conteudo: ptr("olá")},
			{Papel: store.PapelHumano, Conteudo: ptr("atendo eu")},
			{Papel: store.PapelCliente, Conteudo: nil, Anexos: anexos[:1]},
		} {
			m.CriadaEm = base.Add(time.Duration(i) * time.Minute)
			if err := s.AnexarMensagem(ctx, id, m); err != nil {
				t.Fatal(err)
			}
		}
		got, err := s.Janela(ctx, id, 10, passado)
		if err != nil || len(got) != 4 {
			t.Fatalf("janela: %v len=%d", err, len(got))
		}
		if got[0].Papel != store.PapelCliente || got[1].Papel != store.PapelAgente || got[2].Papel != store.PapelHumano {
			t.Errorf("papéis: %s %s %s", got[0].Papel, got[1].Papel, got[2].Papel)
		}
		want := []mensagem.Anexo{
			{Tipo: "audio", Mime: "audio/ogg", Tamanho: 1234, DuracaoS: 12, Encaminhado: true},
			{Tipo: "outro", TipoOriginal: "sticker"},
		}
		if fmt.Sprint(got[0].Anexos) != fmt.Sprint(want) {
			t.Errorf("anexos = %+v, quer %+v (sem Ref)", got[0].Anexos, want)
		}
		if len(got[1].Anexos) != 0 {
			t.Errorf("mensagem sem anexo voltou com %v", got[1].Anexos)
		}
		if got[3].Conteudo != nil {
			t.Error("conteúdo nil deveria voltar nil")
		}
		if !got[0].CriadaEm.Equal(base) {
			t.Errorf("criada_em = %v", got[0].CriadaEm)
		}
	})

	t.Run("isolamento entre tenants e agentes", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		a := store.ConversaID{Agente: "eliza", Tenant: "org-A", Contato: "5511999990001"}
		b := store.ConversaID{Agente: "eliza", Tenant: "org-B", Contato: "5511999990001"}
		c := store.ConversaID{Agente: "site", Tenant: "org-A", Contato: "5511999990001"}
		cli(t, s, a, "segredo de A", 1)
		cli(t, s, b, "segredo de B", 1)
		cli(t, s, c, "segredo do site", 1)
		_ = s.Pausar(ctx, a, futuro)
		_ = s.GuardarConfig(ctx, a, []byte(`{"de":"A"}`))

		for nome, outro := range map[string]store.ConversaID{"outro tenant": b, "outro agente": c} {
			conv, err := s.Conversa(ctx, outro)
			if err != nil {
				t.Fatal(err)
			}
			if conv.PausadaAte != nil || conv.ConfigJSON != nil {
				t.Errorf("%s enxerga pausa/config de A: %+v", nome, conv)
			}
		}
		for nome, par := range map[string]struct {
			id   store.ConversaID
			quer string
		}{"A": {a, "segredo de A"}, "B": {b, "segredo de B"}, "site": {c, "segredo do site"}} {
			got, _ := s.Janela(ctx, par.id, 10, passado)
			if !igual(textos(got), []string{par.quer}) {
				t.Errorf("janela de %s = %v", nome, textos(got))
			}
		}
		// apagar B não toca A nem o site
		if err := s.ApagarContato(ctx, b); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.Janela(ctx, a, 10, passado); len(got) != 1 {
			t.Errorf("apagar B afetou A: %v", textos(got))
		}
		if got, _ := s.Janela(ctx, c, 10, passado); len(got) != 1 {
			t.Errorf("apagar B afetou o site: %v", textos(got))
		}
		if conv, _ := s.Conversa(ctx, a); conv.PausadaAte == nil {
			t.Error("apagar B zerou a pausa de A")
		}
	})

	t.Run("retenção deixa a linha com conteúdo nil", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		cli(t, s, id, "velha", 0)
		cli(t, s, id, "nova", 60)
		_ = s.AnexarMensagem(ctx, id, store.MensagemMemoria{
			Papel: store.PapelCliente, CriadaEm: base, Anexos: []mensagem.Anexo{{Tipo: "audio", DuracaoS: 3}},
		})
		n, err := s.ExpurgarTexto(ctx, base.Add(30*time.Minute))
		if err != nil || n != 1 {
			t.Fatalf("expurgo: n=%d err=%v (a sem conteúdo não conta)", n, err)
		}
		got, _ := s.Janela(ctx, id, 10, passado)
		if len(got) != 3 {
			t.Fatalf("a linha tem de ficar: len=%d", len(got))
		}
		var nils, anexos int
		for _, m := range got {
			if m.Conteudo == nil {
				nils++
			}
			anexos += len(m.Anexos)
		}
		if nils != 2 || texto(got[2]) != "nova" {
			t.Errorf("depois do expurgo: %v", textos(got))
		}
		if anexos != 1 {
			t.Errorf("anexos devem ficar: %d", anexos)
		}
		if n, _ := s.ExpurgarTexto(ctx, base.Add(30*time.Minute)); n != 0 {
			t.Errorf("segundo expurgo mudou %d linhas", n)
		}
	})

	t.Run("eventos e ecos antigos são apagados", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		_, _ = s.MarcarEvento(ctx, "eliza", "M1")
		_ = s.RegistrarEco(ctx, "eliza", "R1")
		if n, err := s.ApagarEventosEcos(ctx, passado); err != nil || n != 0 {
			t.Fatalf("nada é anterior a 1970: n=%d err=%v", n, err)
		}
		if n, err := s.ApagarEventosEcos(ctx, futuro); err != nil || n != 2 {
			t.Fatalf("apagar tudo: n=%d err=%v", n, err)
		}
		if novo, _ := s.MarcarEvento(ctx, "eliza", "M1"); !novo {
			t.Error("evento deveria ter sido apagado")
		}
		if eco, _ := s.EhEco(ctx, "eliza", "R1"); eco {
			t.Error("eco deveria ter sido apagado")
		}
	})

	t.Run("apagamento do contato", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		cli(t, s, id, "oi", 1)
		_ = s.Pausar(ctx, id, futuro)
		for i := 0; i < 2; i++ {
			err := s.RegistrarTurno(ctx, store.Turno{
				ConversaID: id, Modo: store.ModoLLM, MensagemIDs: []string{"M1"}, Resultado: "ok",
			})
			if err != nil {
				t.Fatal(err)
			}
		}
		if err := s.ApagarContato(ctx, id); err != nil {
			t.Fatal(err)
		}
		if got, _ := s.Janela(ctx, id, 10, passado); len(got) != 0 {
			t.Errorf("mensagens sobraram: %v", textos(got))
		}
		if c, _ := s.Conversa(ctx, id); c.PausadaAte != nil {
			t.Error("a conversa devia voltar vazia")
		}
		// idempotente, inclusive para contato que nunca existiu
		if err := s.ApagarContato(ctx, id); err != nil {
			t.Errorf("segunda chamada: %v", err)
		}
		nunca := store.ConversaID{Agente: "eliza", Tenant: "org-1", Contato: "000"}
		if err := s.ApagarContato(ctx, nunca); err != nil {
			t.Errorf("contato inexistente: %v", err)
		}
	})

	t.Run("turno", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		err := s.RegistrarTurno(ctx, store.Turno{
			ConversaID:    id,
			Modo:          store.ModoLLM,
			MensagemIDs:   []string{"M1", "M2"},
			Modelo:        "gemini-2.5-flash",
			Iteracoes:     2,
			Ferramentas:   []store.FerramentaLog{{Nome: "criarAgendamento", Status: 201}},
			TokensEntrada: 100, TokensSaida: 20, CustoMicroUSD: 55, LatenciaMS: 1200,
			Resultado: "ok", Escalou: true, CriadoEm: base,
		})
		if err != nil {
			t.Fatal(err)
		}
		// só o mínimo, sem listas
		if err := s.RegistrarTurno(ctx, store.Turno{ConversaID: id, Modo: store.ModoPausado, Resultado: "ok"}); err != nil {
			t.Fatal(err)
		}
		if err := s.RegistrarTurno(ctx, store.Turno{ConversaID: id, Modo: "xyz", Resultado: "ok"}); !errors.Is(err, store.ErrModoInvalido) {
			t.Errorf("modo inválido: %v", err)
		}
	})

	t.Run("jobs", func(t *testing.T) {
		s := nova(t)
		defer s.Close()
		if ok, err := s.JobFeito(ctx, "retencao", "2026-10-09"); err != nil || ok {
			t.Fatalf("antes: ok=%v err=%v", ok, err)
		}
		if err := s.MarcarJob(ctx, "retencao", "2026-10-09"); err != nil {
			t.Fatal(err)
		}
		if err := s.MarcarJob(ctx, "retencao", "2026-10-09"); err != nil {
			t.Fatalf("marcar duas vezes: %v", err)
		}
		if ok, _ := s.JobFeito(ctx, "retencao", "2026-10-09"); !ok {
			t.Error("deveria estar feito")
		}
		if ok, _ := s.JobFeito(ctx, "retencao", "2026-10-10"); ok {
			t.Error("outro dia não está feito")
		}
		if ok, _ := s.JobFeito(ctx, "outro", "2026-10-09"); ok {
			t.Error("outro job não está feito")
		}
	})
}
