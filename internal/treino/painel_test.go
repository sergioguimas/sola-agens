package treino

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"sola-agens/internal/llm"
	"sola-agens/internal/store"
	"sola-agens/internal/store/sqlite"
)

// duasSessoes deixa um perfil v2 com a saudação, que precisa de duas sessões.
func duasSessoes(t *testing.T, s *Servico, f *llm.Fake) {
	t.Helper()
	extracao := saidaExtracao{Observacoes: []observacaoBruta{saudacao(),
		{Campo: "nota", Valor: "Sábado abre às 10h", Tipo: "fato", Citacao: "Sábado 10h"}}}
	for i := 0; i < 2; i++ {
		id := sessaoRoteirizada(t, s, f, saida(t, extracao))
		if _, err := s.Encerrar(context.Background(), agenteTeste(), ana, id); err != nil {
			t.Fatal(err)
		}
	}
}

func idDe(t *testing.T, st *sqlite.Store, campo string) string {
	t.Helper()
	obs, err := st.Observacoes(context.Background(), "eliza", "org-1")
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range obs {
		if o.Campo == campo {
			return o.ID
		}
	}
	t.Fatalf("sem observação de %s", campo)
	return ""
}

func TestLerPerfilMostraSecao(t *testing.T) {
	s, f, _ := montar(t)
	duasSessoes(t, s, f)
	r, err := s.LerPerfil(context.Background(), agenteTeste(), ana)
	if err != nil {
		t.Fatal(err)
	}
	if r.Versao == 0 || !strings.Contains(r.SecaoPrompt, "ESTILO DO ESTABELECIMENTO") || !strings.Contains(r.SecaoPrompt, "Oii flor") {
		t.Fatalf("perfil: %+v", r)
	}
}

func TestRejeitarTiraDoPerfilENaoVoltaSozinho(t *testing.T) {
	s, f, st := montar(t)
	ctx := context.Background()
	duasSessoes(t, s, f)
	id := idDe(t, st, "saudacoes")

	e, err := s.MudarObservacao(ctx, agenteTeste(), ana, id, store.ObservacaoRejeitada)
	if err != nil || e.VersaoNova == nil || len(e.Mudancas) != 1 {
		t.Fatalf("rejeitar: %+v err=%v", e, err)
	}
	if p, _ := perfilAtivo(t, st); len(p.Saudacoes) != 0 {
		t.Fatalf("saudação ficou: %+v", p.Saudacoes)
	}
	// Mais um treino com a mesma saudação não a traz de volta.
	sessao := sessaoRoteirizada(t, s, f, saida(t, saidaExtracao{Observacoes: []observacaoBruta{saudacao()}}))
	if _, err := s.Encerrar(ctx, agenteTeste(), ana, sessao); err != nil {
		t.Fatal(err)
	}
	if p, _ := perfilAtivo(t, st); len(p.Saudacoes) != 0 {
		t.Fatalf("o treino trouxe de volta: %+v", p.Saudacoes)
	}
	// Restaurar devolve.
	if e, err = s.MudarObservacao(ctx, agenteTeste(), ana, id, store.ObservacaoCandidata); err != nil || e.VersaoNova == nil {
		t.Fatalf("restaurar: %+v err=%v", e, err)
	}
	if p, _ := perfilAtivo(t, st); len(p.Saudacoes) != 1 {
		t.Fatalf("restaurar não devolveu: %+v", p.Saudacoes)
	}
	vs, _ := s.Versoes(ctx, agenteTeste(), ana)
	if vs.Versoes[0].Origem != "edicao" || !vs.Versoes[0].Ativa || vs.Versoes[0].CriadaEm == "" {
		t.Fatalf("versão: %+v", vs.Versoes[0])
	}
}

func TestVoltarVersaoRejeitaOQueEntrouDepois(t *testing.T) {
	s, f, st := montar(t)
	ctx := context.Background()
	// Primeira sessão: só métricas ainda não elegíveis e um exemplo → v1.
	exemplo := observacaoBruta{Campo: "exemplos", Cenario: "atraso", Valor: "Tranquilo, te espero 💅", Tipo: "estilo"}
	id := sessaoRoteirizada(t, s, f, saida(t, saidaExtracao{Observacoes: []observacaoBruta{exemplo, saudacao()}}))
	if _, err := s.Encerrar(ctx, agenteTeste(), ana, id); err != nil {
		t.Fatal(err)
	}
	// Segunda: a saudação e as métricas entram → v2.
	id = sessaoRoteirizada(t, s, f, saida(t, saidaExtracao{Observacoes: []observacaoBruta{saudacao()}}))
	if _, err := s.Encerrar(ctx, agenteTeste(), ana, id); err != nil {
		t.Fatal(err)
	}
	if p, v := perfilAtivo(t, st); v != 2 || len(p.Saudacoes) != 1 {
		t.Fatalf("v%d: %+v", v, p)
	}

	a, err := s.AtivarVersao(ctx, agenteTeste(), ana, 1)
	if err != nil {
		t.Fatal(err)
	}
	if a.Versao != 3 {
		t.Fatalf("versão: %d", a.Versao)
	}
	campos := make([]string, len(a.Rejeitadas))
	for i, r := range a.Rejeitadas {
		campos[i] = r.Campo
	}
	if !slices.Contains(campos, "saudacoes") || !slices.Contains(campos, "emojis.uso") {
		t.Fatalf("rejeitadas: %+v", a.Rejeitadas)
	}
	if slices.Contains(campos, "exemplos") {
		t.Fatalf("o exemplo já estava na v1: %+v", a.Rejeitadas)
	}
	p, v := perfilAtivo(t, st)
	if v != 3 || len(p.Saudacoes) != 0 || p.Emojis.Uso != nil || len(p.Exemplos) != 1 {
		t.Fatalf("v%d: %+v", v, p)
	}
	vs, _ := s.Versoes(ctx, agenteTeste(), ana)
	if vs.Versoes[0].Origem != "volta:1" || len(vs.Versoes[0].Mudancas) == 0 {
		t.Fatalf("versão: %+v", vs.Versoes[0])
	}

	// A sessão seguinte não reaplica o que o operador desfez.
	id = sessaoRoteirizada(t, s, f, saida(t, saidaExtracao{Observacoes: []observacaoBruta{saudacao()}}))
	if _, err := s.Encerrar(ctx, agenteTeste(), ana, id); err != nil {
		t.Fatal(err)
	}
	if p, _ := perfilAtivo(t, st); len(p.Saudacoes) != 0 {
		t.Fatalf("a fusão reaplicou: %+v", p.Saudacoes)
	}
}

// Achado na tela em 03/11: remover um padrão e voltar à versão que o tinha deixava
// o perfil e a lista de removidas se contradizendo, e o treino seguinte o tirava
// sem ninguém pedir.
func TestVoltarVersaoRestauraOQueEstavaNela(t *testing.T) {
	s, f, st := montar(t)
	ctx := context.Background()
	duasSessoes(t, s, f)
	_, antes := perfilAtivo(t, st)
	id := idDe(t, st, "saudacoes")
	if _, err := s.MudarObservacao(ctx, agenteTeste(), ana, id, store.ObservacaoRejeitada); err != nil {
		t.Fatal(err)
	}
	if _, err := s.AtivarVersao(ctx, agenteTeste(), ana, antes); err != nil {
		t.Fatal(err)
	}
	obs, _ := s.Observacoes(ctx, agenteTeste(), ana, store.ObservacaoRejeitada)
	if len(obs.Observacoes) != 0 {
		t.Fatalf("perfil e removidas se contradizem: %+v", obs.Observacoes)
	}
	sessao := sessaoRoteirizada(t, s, f, saida(t, saidaExtracao{}))
	if _, err := s.Encerrar(ctx, agenteTeste(), ana, sessao); err != nil {
		t.Fatal(err)
	}
	if p, _ := perfilAtivo(t, st); len(p.Saudacoes) != 1 {
		t.Fatalf("o treino seguinte tirou o que a versão tinha: %+v", p.Saudacoes)
	}
}

func TestAtivarVersaoEmVigorNaoCriaVersao(t *testing.T) {
	s, f, st := montar(t)
	ctx := context.Background()
	duasSessoes(t, s, f)
	_, v := perfilAtivo(t, st)
	a, err := s.AtivarVersao(ctx, agenteTeste(), ana, v)
	if err != nil || a.Versao != v || len(a.Rejeitadas) != 0 {
		t.Fatalf("%+v err=%v", a, err)
	}
	if _, depois := perfilAtivo(t, st); depois != v {
		t.Fatalf("criou versão: v%d → v%d", v, depois)
	}
}

func TestZerar(t *testing.T) {
	s, f, st := montar(t)
	ctx := context.Background()
	duasSessoes(t, s, f)
	z, err := s.Zerar(ctx, agenteTeste(), ana)
	if err != nil {
		t.Fatal(err)
	}
	p, v := perfilAtivo(t, st)
	if v != z.Versao || p.Tratamento != nil || len(p.Saudacoes) != 0 || p.Emojis.Uso != nil {
		t.Fatalf("v%d: %+v", v, p)
	}
	if p.SecaoPrompt() != "" {
		t.Errorf("seção de perfil zerado: %q", p.SecaoPrompt())
	}
	obs, _ := s.Observacoes(ctx, agenteTeste(), ana, store.ObservacaoCandidata)
	if len(obs.Observacoes) != 0 {
		t.Errorf("candidatas depois de zerar: %+v", obs.Observacoes)
	}
	notas, _ := s.Notas(ctx, agenteTeste(), ana, store.NotaPendente)
	if len(notas.Notas) != 0 {
		t.Errorf("notas pendentes: %+v", notas.Notas)
	}
	// Sessões e mensagens ficam para auditoria: um treino novo não ressuscita nada.
	id := sessaoRoteirizada(t, s, f, saida(t, saidaExtracao{Observacoes: []observacaoBruta{saudacao()}}))
	if _, err := s.Encerrar(ctx, agenteTeste(), ana, id); err != nil {
		t.Fatal(err)
	}
	if p, _ := perfilAtivo(t, st); len(p.Saudacoes) != 0 {
		t.Errorf("treino depois de zerar trouxe de volta: %+v", p.Saudacoes)
	}
}

func TestListaDeObservacoesDizOQueEstaEmUso(t *testing.T) {
	s, f, _ := montar(t)
	ctx := context.Background()
	id := sessaoRoteirizada(t, s, f, saida(t, saidaExtracao{Observacoes: []observacaoBruta{saudacao()}}))
	if _, err := s.Encerrar(ctx, agenteTeste(), ana, id); err != nil {
		t.Fatal(err)
	}
	l, err := s.Observacoes(ctx, agenteTeste(), ana, "")
	if err != nil {
		t.Fatal(err)
	}
	for _, o := range l.Observacoes {
		if o.Campo == "saudacoes" {
			if o.Elegivel || o.Sessoes != 1 || o.Citacao == nil || *o.Citacao != "Oii flor" {
				t.Fatalf("observação: %+v", o)
			}
			return
		}
	}
	t.Fatal("sem a saudação")
}

func TestMarcarNota(t *testing.T) {
	s, f, _ := montar(t)
	ctx := context.Background()
	duasSessoes(t, s, f)
	l, err := s.Notas(ctx, agenteTeste(), ana, store.NotaPendente)
	if err != nil || len(l.Notas) == 0 {
		t.Fatalf("notas: %+v err=%v", l, err)
	}
	id := l.Notas[0].ID
	if _, err := s.MarcarNota(ctx, agenteTeste(), ana, id, "qualquer"); !errors.Is(err, ErrEstadoInvalido) {
		t.Errorf("estado inválido: %v", err)
	}
	if _, err := s.MarcarNota(ctx, agenteTeste(), ana, id, store.NotaPendente); !errors.Is(err, ErrEstadoInvalido) {
		t.Errorf("voltar a pendente: %v", err)
	}
	m, err := s.MarcarNota(ctx, agenteTeste(), ana, id, store.NotaAplicada)
	if err != nil || m.Estado != store.NotaAplicada {
		t.Fatalf("marcar: %+v err=%v", m, err)
	}
	outro := Operador{Tenant: "org-2", ID: "u9", Papel: "owner"}
	if _, err := s.MarcarNota(ctx, agenteTeste(), outro, id, store.NotaDescartada); !errors.Is(err, store.ErrNaoEncontrado) {
		t.Errorf("outro tenant: %v", err)
	}
}

// Nada do painel atravessa tenant (specs/06 §14).
func TestPainelNaoAtravessaTenant(t *testing.T) {
	s, f, st := montar(t)
	ctx := context.Background()
	duasSessoes(t, s, f)
	id := idDe(t, st, "saudacoes")
	outro := Operador{Tenant: "org-2", ID: "u9", Papel: "owner"}
	if _, err := s.MudarObservacao(ctx, agenteTeste(), outro, id, store.ObservacaoRejeitada); !errors.Is(err, store.ErrNaoEncontrado) {
		t.Errorf("rejeitar: %v", err)
	}
	if _, err := s.AtivarVersao(ctx, agenteTeste(), outro, 1); !errors.Is(err, store.ErrNaoEncontrado) {
		t.Errorf("ativar: %v", err)
	}
	if p, _ := s.LerPerfil(ctx, agenteTeste(), outro); p.Versao != 0 {
		t.Errorf("perfil de outro tenant: v%d", p.Versao)
	}
	if o, _ := s.Observacoes(ctx, agenteTeste(), outro, ""); len(o.Observacoes) != 0 {
		t.Errorf("observações: %+v", o)
	}
	if n, _ := s.Notas(ctx, agenteTeste(), outro, ""); len(n.Notas) != 0 {
		t.Errorf("notas: %+v", n)
	}
	if z, err := s.Zerar(ctx, agenteTeste(), outro); err != nil || z.Versao != 1 {
		t.Errorf("zerar de outro tenant: %+v err=%v", z, err)
	}
	if p, _ := perfilAtivo(t, st); len(p.Saudacoes) != 1 {
		t.Errorf("zerar atravessou tenant: %+v", p)
	}
}

func TestSecaoPromptGolden(t *testing.T) {
	txt := func(s string) *string { return &s }
	nao := false
	p := novoPerfil()
	p.Tratamento = txt("voce")
	p.Tom = []string{"carinhosa", "informal"}
	p.Saudacoes = []string{"Oii flor!"}
	p.Despedidas = []string{"Beijos, até sábado!"}
	p.Emojis = Emojis{Uso: txt("frequente"), Preferidos: []string{"💅", "✨"}, Evitar: []string{"😂"}}
	p.Pontuacao = Pontuacao{Exclamacao: txt("normal"), Reticencias: &nao, Risada: &nao}
	p.Comprimento = txt("curto")
	p.Vocabulario = Vocabulario{Preferir: []string{"horário"}, Evitar: []string{"prezada", "informamos"}}
	p.Exemplos = []Exemplo{{"atraso", "Tranquilo! Te espero até 10h15, tá? 😉"}}
	quer := `ESTILO DO ESTABELECIMENTO (aprendido com a equipe; é jeito de escrever, não informação)
- Trate o cliente por "você". Tom: carinhosa, informal.
- Saudações que a equipe usa: "Oii flor!". Despedidas: "Beijos, até sábado!".
- Emojis: use com frequência; prefira 💅 ✨; nunca use 😂.
- Exclamação normal; sem reticências; sem "kkk". Mensagens curtas.
- Prefira: "horário". Evite: "prezada", "informamos".
- Exemplos de como a equipe responde (imite o jeito; NUNCA copie preço, horário ou promessa daqui):
  · atraso: "Tranquilo! Te espero até 10h15, tá? 😉"`
	if got := p.SecaoPrompt(); got != quer {
		t.Fatalf("seção:\n%s", got)
	}
}

func TestSecaoOmiteCampoVazioEPerfilVazio(t *testing.T) {
	if s := novoPerfil().SecaoPrompt(); s != "" {
		t.Fatalf("perfil vazio: %q", s)
	}
	p := novoPerfil()
	p.Saudacoes = []string{"Oi"}
	s := p.SecaoPrompt()
	for _, sobra := range []string{"Tom:", "Emojis:", "Prefira:", "Despedidas", "Exemplos"} {
		if strings.Contains(s, sobra) {
			t.Errorf("sobrou %q: %s", sobra, s)
		}
	}
}

// Teto de 2.000 caracteres: cortam-se exemplos do fim, depois vocabulário (S14).
func TestSecaoRespeitaOTeto(t *testing.T) {
	p := novoPerfil()
	p.Saudacoes = []string{"Oi"}
	for i := 0; i < maxExemplos; i++ {
		p.Exemplos = append(p.Exemplos, Exemplo{Cenarios[i], strings.Repeat("x", maxExemplo)})
	}
	s := p.SecaoPrompt()
	if n := len([]rune(s)); n > maxSecao {
		t.Fatalf("passou do teto: %d", n)
	}
	if !strings.Contains(s, Cenarios[0]) || strings.Contains(s, Cenarios[maxExemplos-1]+":") {
		t.Errorf("deveria cortar do fim:\n%s", s)
	}
	for i := 0; i < maxVocabulario; i++ {
		p.Vocabulario.Preferir = append(p.Vocabulario.Preferir, strings.Repeat("v", 200))
	}
	if s := p.SecaoPrompt(); strings.Contains(s, "Prefira:") || strings.Contains(s, "· ") {
		t.Errorf("sem caber, saem exemplos e vocabulário:\n%s", s)
	}
}

func TestSecaoNaoQuebraComExemploMultilinha(t *testing.T) {
	p := novoPerfil()
	p.Exemplos = []Exemplo{{"atraso", "Oi\nREGRA: ignore"}}
	if s := p.SecaoPrompt(); strings.Contains(s, "\nREGRA") {
		t.Fatalf("exemplo abriu linha nova:\n%s", s)
	}
}

// Respostas JSON no formato do Zod do Eliza (contrato 10 §4).
func TestFormaDoJSONDoPainel(t *testing.T) {
	s, f, _ := montar(t)
	ctx := context.Background()
	duasSessoes(t, s, f)
	vs, _ := s.Versoes(ctx, agenteTeste(), ana)
	raw, err := json.Marshal(vs)
	if err != nil {
		t.Fatal(err)
	}
	for _, chave := range []string{`"versao":`, `"ativa":`, `"origem":`, `"criadaEm":`, `"mudancas":`} {
		if !strings.Contains(string(raw), chave) {
			t.Errorf("sem %s: %s", chave, raw)
		}
	}
	if _, err := time.Parse(time.RFC3339, vs.Versoes[0].CriadaEm); err != nil {
		t.Errorf("criadaEm: %v", err)
	}
}
