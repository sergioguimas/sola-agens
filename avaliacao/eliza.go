package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"time"

	"sola-agens/internal/sistema"
)

// Eliza simulado em processo: serve a spec real e implementa o contrato do
// autoatendimento com estado por cenário (um ticket por cenário). Não é o
// Eliza: só o bastante para o modelo agir e para conferirmos o resultado.

const (
	baseAPI      = "/api/v1/autoatendimento"
	tokenServico = "avaliacao-token-de-servico-0123456789abcdef"
	segredoEnc   = "avaliacao-segredo-de-encaminhamento-0123456789abcdef"

	// Mundo fixo: qua 14/10/2026, 10:00 no relógio local.
	hoje          = "2026-10-14"
	agoraLocal    = "2026-10-14T10:00"
	layoutLocal   = "2006-01-02T15:04"
	antecedenciaM = 120
	janelaDias    = 30
	maxAtivos     = 3
)

type catServico struct {
	Chave, ID, Nome string
	Duracao         int
	Preco           float64
}

type catProf struct {
	Chave, ID, Nome, Especialidade string
	Grade                          []string
}

var catalogoServicos = []catServico{
	{"corte", "5e1a0000-0000-4000-8000-000000000001", "Corte de cabelo", 45, 60},
	{"manicure", "5e1a0000-0000-4000-8000-000000000002", "Manicure", 60, 40},
}

var catalogoProfs = []catProf{
	{"bia", "b1a00000-0000-4000-8000-000000000001", "Bia", "Cabelo e unhas", []string{"09:00", "10:00", "11:00", "14:00", "15:00", "16:00", "17:00"}},
	{"caio", "ca100000-0000-4000-8000-000000000002", "Caio", "Cabelo", []string{"14:00", "15:00", "16:00", "17:00"}},
}

// nomesJaCadastrados existem em outros números em qualquer cenário.
var nomesJaCadastrados = []string{"João Pereira"}

func servicoPorChave(k string) *catServico {
	for i := range catalogoServicos {
		if catalogoServicos[i].Chave == k {
			return &catalogoServicos[i]
		}
	}
	return nil
}
func servicoPorID(id string) *catServico {
	for i := range catalogoServicos {
		if catalogoServicos[i].ID == id {
			return &catalogoServicos[i]
		}
	}
	return nil
}
func profPorChave(k string) *catProf {
	for i := range catalogoProfs {
		if catalogoProfs[i].Chave == k {
			return &catalogoProfs[i]
		}
	}
	return nil
}
func profPorID(id string) *catProf {
	for i := range catalogoProfs {
		if catalogoProfs[i].ID == id {
			return &catalogoProfs[i]
		}
	}
	return nil
}

// Ag é um agendamento no estado do simulado.
type Ag struct {
	ID, Ref, Status, Servico, Prof, Inicio string
	Aguardando, Novo                       bool
}

func (a *Ag) ativo() bool {
	return a.Status == "pending" || a.Status == "scheduled" || a.Status == "confirmed"
}

// Escala é um escalonamento recebido pelo simulado.
type Escala struct{ Motivo, Resumo string }

// Estado é o mundo de um cenário.
type Estado struct {
	cen            Cenario
	situacao       string
	primeiro       string
	nomeCadastro   string
	ags            []*Ag
	cadastroCriado bool
	mensagens      []string
	escalas        []Escala
	falha          bool
}

// ElizaSim é o servidor HTTP do simulado.
type ElizaSim struct {
	srv  *httptest.Server
	spec []byte

	mu      sync.Mutex
	estados map[string]*Estado // por ticket
	seq     int
}

// NovoEliza sobe o simulado servindo a spec dada em <base>/openapi.json.
func NovoEliza(spec []byte) *ElizaSim {
	e := &ElizaSim{spec: spec, estados: map[string]*Estado{}}
	mux := http.NewServeMux()
	mux.HandleFunc("GET "+baseAPI+"/openapi.json", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(e.spec)
	})
	rota := func(padrao string, h func(*Estado, http.ResponseWriter, *http.Request), negocio bool) {
		mux.HandleFunc(padrao, e.autenticar(h, negocio))
	}
	rota("GET "+baseAPI+"/contexto", e.contexto, false)
	rota("POST "+baseAPI+"/mensagens", e.enviarMensagem, false)
	rota("GET "+baseAPI+"/mensagens/{mensagemId}/midia", e.baixarMidia, false)
	rota("POST "+baseAPI+"/escalonamentos", e.escalar, false)

	rota("GET "+baseAPI+"/servicos", e.servicos, true)
	rota("GET "+baseAPI+"/profissionais", e.profissionais, true)
	rota("GET "+baseAPI+"/horarios", e.horarios, true)
	rota("GET "+baseAPI+"/agendamentos", e.listarAgendamentos, true)
	rota("POST "+baseAPI+"/agendamentos", e.criarAgendamento, true)
	rota("POST "+baseAPI+"/agendamentos/{id}/remarcar", e.remarcar, true)
	rota("POST "+baseAPI+"/agendamentos/{id}/cancelar", e.cancelar, true)
	rota("POST "+baseAPI+"/agendamentos/{id}/confirmar", e.confirmar, true)
	rota("GET "+baseAPI+"/cadastro", e.lerCadastro, true)
	rota("POST "+baseAPI+"/cadastro", e.criarCadastro, true)
	rota("PATCH "+baseAPI+"/cadastro", e.atualizarCadastro, true)
	rota("GET "+baseAPI+"/cadastro/nome", e.verificarNome, true)
	e.srv = httptest.NewServer(mux)
	return e
}

func (e *ElizaSim) URL() string { return e.srv.URL }
func (e *ElizaSim) Fechar()     { e.srv.Close() }

// Novo cria o estado do cenário e o liga ao ticket.
func (e *ElizaSim) Novo(c Cenario, ticket string) *Estado {
	e.mu.Lock()
	defer e.mu.Unlock()
	st := &Estado{cen: c, situacao: c.Cliente.Situacao, primeiro: c.Cliente.PrimeiroNome, falha: c.ElizaFalha == "ferramentas"}
	for _, s := range c.Agendamentos {
		e.seq++
		st.ags = append(st.ags, &Ag{ID: fmt.Sprintf("a9e00000-0000-4000-8000-%012d", e.seq), Ref: s.Ref, Status: s.Status,
			Servico: s.Servico, Prof: s.Profissional, Inicio: s.Inicio, Aguardando: s.Aguardando})
	}
	e.estados[ticket] = st
	return st
}

// Esquecer remove o estado do ticket.
func (e *ElizaSim) Esquecer(ticket string) {
	e.mu.Lock()
	delete(e.estados, ticket)
	e.mu.Unlock()
}

// Marca devolve quantas mensagens e escalonamentos já existem (para fatiar por rodada).
func (e *ElizaSim) Marca(st *Estado) (msgs, escalas int) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return len(st.mensagens), len(st.escalas)
}

// Desde devolve o que chegou depois da marca.
func (e *ElizaSim) Desde(st *Estado, msgs, escalas int) ([]string, []Escala) {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), st.mensagens[msgs:]...), append([]Escala(nil), st.escalas[escalas:]...)
}

// --- envelope e autenticação ----------------------------------------------

func (e *ElizaSim) escrever(w http.ResponseWriter, status int, corpo any) {
	w.Header().Set("Content-Type", "application/json")
	e.mu.Lock()
	e.seq++
	w.Header().Set("X-Request-Id", fmt.Sprintf("sim-%d", e.seq))
	e.mu.Unlock()
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(corpo)
}

func (e *ElizaSim) erro(w http.ResponseWriter, status int, code, msg string, detalhes any) {
	body := map[string]any{"code": code, "message": msg, "request_id": "sim"}
	if detalhes != nil {
		body["details"] = detalhes
	}
	e.escrever(w, status, map[string]any{"error": body})
}

// autenticar confere token, versão e ticket; negocio=true respeita eliza_falha.
func (e *ElizaSim) autenticar(h func(*Estado, http.ResponseWriter, *http.Request), negocio bool) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer "+tokenServico {
			e.erro(w, 401, "UNAUTHORIZED", "token inválido", nil)
			return
		}
		if r.Header.Get("X-Autoatendimento-Versao") != "1" {
			e.erro(w, 400, "VERSION_MISMATCH", "versão do contrato", nil)
			return
		}
		e.mu.Lock()
		st := e.estados[r.Header.Get("X-Autoatendimento-Ticket")]
		e.mu.Unlock()
		if st == nil {
			e.erro(w, 401, "TICKET_INVALID", "ticket inválido", nil)
			return
		}
		if negocio && st.falha {
			e.erro(w, 503, "INTERNAL_ERROR", "indisponível", nil)
			return
		}
		e.mu.Lock()
		defer e.mu.Unlock()
		h(st, w, r)
	}
}

// Os handlers rodam com e.mu travado: escrever() também trava, então eles
// usam as versões "Locked" abaixo.

func (e *ElizaSim) escreverL(w http.ResponseWriter, status int, corpo any) {
	e.seq++
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("X-Request-Id", fmt.Sprintf("sim-%d", e.seq))
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(corpo)
}

func (e *ElizaSim) okL(w http.ResponseWriter, status int, d any) {
	e.escreverL(w, status, map[string]any{"data": d})
}

func (e *ElizaSim) falhaL(w http.ResponseWriter, status int, code, msg string, detalhes any) {
	body := map[string]any{"code": code, "message": msg, "request_id": "sim"}
	if detalhes != nil {
		body["details"] = detalhes
	}
	e.escreverL(w, status, map[string]any{"error": body})
}

// ler decodifica o corpo de forma estrita (campo desconhecido = 422, como o Eliza).
func ler(r *http.Request, v any) error {
	d := json.NewDecoder(r.Body)
	d.DisallowUnknownFields()
	if err := d.Decode(v); err != nil && !errors.Is(err, io.EOF) {
		return err
	}
	return nil
}

// --- tempo -----------------------------------------------------------------

func momento(local string) sistema.Momento {
	t, _ := time.Parse(layoutLocal, local)
	return sistema.Momento{UTC: t.Add(3 * time.Hour).Format("2006-01-02T15:04:05.000Z"), Local: local}
}

func somarMin(local string, min int) string {
	t, _ := time.Parse(layoutLocal, local)
	return t.Add(time.Duration(min) * time.Minute).Format(layoutLocal)
}

// livres devolve os horários de início livres do profissional no dia, e o
// motivo quando não sobra nenhum.
func (st *Estado) livres(prof *catProf, data, ignorarID string) ([]string, string) {
	d, err := time.Parse("2006-01-02", data)
	if err != nil || d.Weekday() == time.Sunday {
		return []string{}, "organization_closed_day"
	}
	agora, _ := time.Parse(layoutLocal, agoraLocal)
	limite := agora.Add(antecedenciaM * time.Minute)
	out := []string{}
	cortou := false
	for _, h := range prof.Grade {
		t, _ := time.Parse(layoutLocal, data+"T"+h)
		if t.Before(limite) {
			cortou = true
			continue
		}
		if st.ocupado(prof.Chave, data+"T"+h, ignorarID) {
			continue
		}
		out = append(out, h)
	}
	if len(out) == 0 {
		if cortou {
			return out, "antecedencia_minima"
		}
		return out, "fully_booked"
	}
	return out, ""
}

func (st *Estado) ocupado(prof, inicio, ignorarID string) bool {
	for _, a := range st.ags {
		if a.ID != ignorarID && a.ativo() && a.Prof == prof && a.Inicio == inicio {
			return true
		}
	}
	return false
}

func (st *Estado) ativos() []*Ag {
	var out []*Ag
	for _, a := range st.ags {
		if a.ativo() {
			out = append(out, a)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Inicio < out[j].Inicio })
	return out
}

func (st *Estado) achar(id string) *Ag {
	for _, a := range st.ags {
		if a.ID == id {
			return a
		}
	}
	return nil
}

func (st *Estado) resumo(a *Ag) sistema.AgendamentoResumo {
	sv, pf := servicoPorChave(a.Servico), profPorChave(a.Prof)
	return sistema.AgendamentoResumo{
		ID: a.ID, Status: a.Status, Inicio: momento(a.Inicio), Fim: momento(somarMin(a.Inicio, sv.Duracao)),
		Servico: &sistema.Ref{ID: sv.ID, Nome: sv.Nome}, Profissional: &sistema.Ref{ID: pf.ID, Nome: pf.Nome},
		PodeCancelar: a.ativo(), PodeRemarcar: a.ativo(), PodeConfirmar: a.Status == "scheduled",
	}
}

// validarInicio aplica janela → antecedência → grade, na ordem do Eliza.
// Devolve (status, código, mensagem, detalhes) com status 0 = válido.
func (st *Estado) validarInicio(prof *catProf, inicio, ignorarID string) (int, string, string, any) {
	t, err := time.Parse(layoutLocal, inicio)
	if err != nil {
		return 422, "VALIDATION_ERROR", "inicio deve ser AAAA-MM-DDTHH:mm (relógio local)", nil
	}
	h, _ := time.Parse("2006-01-02", hoje)
	dia := t.Truncate(24 * time.Hour)
	if dia.Before(h) || dia.After(h.AddDate(0, 0, janelaDias)) {
		return 422, "OUT_OF_WINDOW", "fora da janela de agendamento (até 30 dias à frente)", nil
	}
	agora, _ := time.Parse(layoutLocal, agoraLocal)
	if t.Before(agora.Add(antecedenciaM * time.Minute)) {
		return 422, "NOTICE_TOO_SHORT", "antecedência mínima de 120 minutos", nil
	}
	data, hora := inicio[:10], inicio[11:]
	livres, _ := st.livres(prof, data, ignorarID)
	for _, l := range livres {
		if l == hora {
			return 0, "", "", nil
		}
	}
	sug := livres
	if len(sug) > 3 {
		sug = sug[:3]
	}
	return 409, "SLOT_UNAVAILABLE", "horário indisponível", map[string]any{"sugestoes": sug}
}

// --- operações do motor ----------------------------------------------------

func (e *ElizaSim) contexto(st *Estado, w http.ResponseWriter, _ *http.Request) {
	abre, fecha := "09:00", "18:00"
	ins := "Atendemos só com hora marcada."
	ctx := sistema.Contexto{
		Agora:       momento(agoraLocal),
		FusoHorario: "America/Sao_Paulo",
		Organizacao: sistema.Organizacao{Nome: "Studio Bela", Nicho: "beleza", Termos: sistema.Termos{
			Genero: "m", Cliente: "cliente", ClientePlural: "clientes", Profissional: "profissional",
			ProfissionalPlural: "profissionais", Servico: "serviço", ServicoPlural: "serviços",
			Agendamento: "agendamento", AgendamentoPlural: "agendamentos"}},
		Expediente: sistema.Expediente{DiasDaSemana: []int{1, 2, 3, 4, 5, 6}, Abertura: &abre, Fechamento: &fecha,
			Almoco: &sistema.Almoco{Inicio: "12:00", Fim: "13:00"}},
		Politica: sistema.Politica{AntecedenciaMinimaMinutos: antecedenciaM, JanelaMaximaDias: janelaDias,
			MaxAgendamentosAtivos: maxAtivos, InstrucoesAtendimento: &ins},
		Identificacao:         sistema.Identificacao{Situacao: st.situacao},
		Agendamentos:          []sistema.AgendamentoResumo{},
		AguardandoConfirmacao: []string{},
	}
	if st.situacao == sistema.SituacaoIdentificado {
		ctx.Identificacao.Cliente = &sistema.ClienteIdentificado{ID: "c0000000-0000-4000-8000-000000000001", PrimeiroNome: st.primeiro}
		for _, a := range st.ativos() {
			ctx.Agendamentos = append(ctx.Agendamentos, st.resumo(a))
			if a.Aguardando && a.Status == "scheduled" {
				ctx.AguardandoConfirmacao = append(ctx.AguardandoConfirmacao, a.ID)
			}
		}
	}
	if p := st.cen.Atendente; p != nil {
		if p.Persona != "" {
			ctx.Atendente.Persona = &p.Persona
		}
		if p.Tom != "" {
			ctx.Atendente.Tom = &p.Tom
		}
	}
	e.okL(w, 200, ctx)
}

func (e *ElizaSim) enviarMensagem(st *Estado, w http.ResponseWriter, r *http.Request) {
	var b struct {
		Texto string `json:"texto"`
	}
	if err := ler(r, &b); err != nil || strings.TrimSpace(b.Texto) == "" {
		e.falhaL(w, 422, "VALIDATION_ERROR", "texto obrigatório", nil)
		return
	}
	st.mensagens = append(st.mensagens, b.Texto)
	e.okL(w, 201, map[string]any{"mensagemId": fmt.Sprintf("out-%d", len(st.mensagens))})
}

func (e *ElizaSim) baixarMidia(_ *Estado, w http.ResponseWriter, _ *http.Request) {
	e.falhaL(w, 404, "NOT_FOUND", "mídia não encontrada", nil)
}

func (e *ElizaSim) escalar(st *Estado, w http.ResponseWriter, r *http.Request) {
	var b struct {
		Motivo string `json:"motivo"`
		Resumo string `json:"resumo"`
	}
	if err := ler(r, &b); err != nil || strings.TrimSpace(b.Resumo) == "" || !motivosEscala[b.Motivo] {
		e.falhaL(w, 422, "VALIDATION_ERROR", "motivo ou resumo inválido", nil)
		return
	}
	st.escalas = append(st.escalas, Escala{b.Motivo, b.Resumo})
	e.okL(w, 201, map[string]any{"equipeNotificada": true})
}

// --- ferramentas -----------------------------------------------------------

func (e *ElizaSim) servicos(_ *Estado, w http.ResponseWriter, _ *http.Request) {
	l := []map[string]any{}
	for _, s := range catalogoServicos {
		l = append(l, map[string]any{"id": s.ID, "nome": s.Nome, "descricao": nil, "duracaoMinutos": s.Duracao, "preco": s.Preco})
	}
	e.okL(w, 200, map[string]any{"servicos": l})
}

func profJSON(p catProf) map[string]any {
	return map[string]any{"id": p.ID, "nome": p.Nome, "especialidade": p.Especialidade}
}

func (e *ElizaSim) profissionais(_ *Estado, w http.ResponseWriter, _ *http.Request) {
	l := []map[string]any{}
	for _, p := range catalogoProfs {
		l = append(l, profJSON(p))
	}
	e.okL(w, 200, map[string]any{"profissionais": l})
}

func (e *ElizaSim) horarios(st *Estado, w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	sv := servicoPorID(q.Get("servicoId"))
	if sv == nil {
		e.falhaL(w, 404, "NOT_FOUND", "serviço não encontrado", nil)
		return
	}
	data := q.Get("data")
	d, err := time.Parse("2006-01-02", data)
	if err != nil {
		e.falhaL(w, 422, "VALIDATION_ERROR", "data deve ser AAAA-MM-DD", nil)
		return
	}
	h, _ := time.Parse("2006-01-02", hoje)
	if d.Before(h) || d.After(h.AddDate(0, 0, janelaDias)) {
		e.falhaL(w, 422, "OUT_OF_WINDOW", "data fora da janela de agendamento", nil)
		return
	}
	filtro := q.Get("profissionalId")
	if filtro != "" && profPorID(filtro) == nil {
		e.falhaL(w, 404, "NOT_FOUND", "profissional não encontrado", nil)
		return
	}
	bloco := []map[string]any{}
	for _, p := range catalogoProfs {
		if filtro != "" && p.ID != filtro {
			continue
		}
		hs, motivo := st.livres(&p, data, "")
		var mv any
		if motivo != "" {
			mv = motivo
		}
		bloco = append(bloco, map[string]any{"profissional": profJSON(p), "horarios": hs, "motivoVazio": mv})
	}
	e.okL(w, 200, map[string]any{"data": data, "servicoId": sv.ID, "porProfissional": bloco})
}

func (e *ElizaSim) listarAgendamentos(st *Estado, w http.ResponseWriter, _ *http.Request) {
	if !e.exigeIdentificado(st, w) {
		return
	}
	l := []sistema.AgendamentoResumo{}
	for _, a := range st.ativos() {
		l = append(l, st.resumo(a))
	}
	e.okL(w, 200, map[string]any{"agendamentos": l})
}

// exigeIdentificado responde o erro de regra e devolve false se não for identificado.
func (e *ElizaSim) exigeIdentificado(st *Estado, w http.ResponseWriter) bool {
	switch st.situacao {
	case sistema.SituacaoIdentificado:
		return true
	case sistema.SituacaoAmbiguo:
		e.falhaL(w, 409, "CUSTOMER_AMBIGUOUS", "mais de um cadastro neste número", nil)
	default:
		e.falhaL(w, 403, "CUSTOMER_NOT_IDENTIFIED", "cliente não identificado: cadastre antes", nil)
	}
	return false
}

func (e *ElizaSim) criarAgendamento(st *Estado, w http.ResponseWriter, r *http.Request) {
	var b struct {
		ServicoID      string  `json:"servicoId"`
		ProfissionalID string  `json:"profissionalId"`
		Inicio         string  `json:"inicio"`
		Observacao     *string `json:"observacao"`
	}
	if err := ler(r, &b); err != nil || b.ServicoID == "" || b.ProfissionalID == "" || b.Inicio == "" {
		e.falhaL(w, 422, "VALIDATION_ERROR", "servicoId, profissionalId e inicio são obrigatórios", nil)
		return
	}
	if !e.exigeIdentificado(st, w) {
		return
	}
	sv, pf := servicoPorID(b.ServicoID), profPorID(b.ProfissionalID)
	if sv == nil || pf == nil {
		e.falhaL(w, 404, "NOT_FOUND", "serviço ou profissional não encontrado", nil)
		return
	}
	for _, a := range st.ativos() { // idempotência: mesmo profissional e início
		if a.Prof == pf.Chave && a.Inicio == b.Inicio && a.Servico == sv.Chave {
			e.okL(w, 201, map[string]any{"agendamento": st.resumo(a)})
			return
		}
	}
	if len(st.ativos()) >= maxAtivos {
		e.falhaL(w, 409, "ACTIVE_LIMIT_REACHED", "limite de agendamentos ativos atingido", nil)
		return
	}
	if status, code, msg, det := st.validarInicio(pf, b.Inicio, ""); status != 0 {
		e.falhaL(w, status, code, msg, det)
		return
	}
	e.seq++
	a := &Ag{ID: fmt.Sprintf("a9e00000-0000-4000-8000-%012d", e.seq), Status: "pending", Servico: sv.Chave, Prof: pf.Chave,
		Inicio: b.Inicio, Novo: true}
	st.ags = append(st.ags, a)
	e.okL(w, 201, map[string]any{"agendamento": st.resumo(a)})
}

// agendamentoDoPath acha o agendamento do cliente; responde 404 se não existe.
func (e *ElizaSim) agendamentoDoPath(st *Estado, w http.ResponseWriter, r *http.Request) *Ag {
	if !e.exigeIdentificado(st, w) {
		return nil
	}
	a := st.achar(r.PathValue("id"))
	if a == nil {
		e.falhaL(w, 404, "NOT_FOUND", "agendamento não encontrado", nil)
	}
	return a
}

func (e *ElizaSim) foraDaAntecedencia(a *Ag) bool {
	ini, _ := time.Parse(layoutLocal, a.Inicio)
	agora, _ := time.Parse(layoutLocal, agoraLocal)
	return ini.Before(agora.Add(antecedenciaM * time.Minute))
}

func (e *ElizaSim) remarcar(st *Estado, w http.ResponseWriter, r *http.Request) {
	var b struct {
		Inicio         string `json:"inicio"`
		ProfissionalID string `json:"profissionalId"`
	}
	if err := ler(r, &b); err != nil || b.Inicio == "" {
		e.falhaL(w, 422, "VALIDATION_ERROR", "inicio é obrigatório", nil)
		return
	}
	a := e.agendamentoDoPath(st, w, r)
	if a == nil {
		return
	}
	if !a.ativo() {
		e.falhaL(w, 409, "INVALID_TRANSITION", "este agendamento não pode ser remarcado", nil)
		return
	}
	if e.foraDaAntecedencia(a) {
		e.falhaL(w, 422, "NOTICE_TOO_SHORT", "dentro da antecedência mínima: fale com a equipe", nil)
		return
	}
	pf := profPorChave(a.Prof)
	if b.ProfissionalID != "" {
		if pf = profPorID(b.ProfissionalID); pf == nil {
			e.falhaL(w, 404, "NOT_FOUND", "profissional não encontrado", nil)
			return
		}
	}
	if status, code, msg, det := st.validarInicio(pf, b.Inicio, a.ID); status != 0 {
		e.falhaL(w, status, code, msg, det)
		return
	}
	a.Inicio, a.Prof, a.Status, a.Aguardando = b.Inicio, pf.Chave, "pending", false
	e.okL(w, 200, map[string]any{"agendamento": st.resumo(a)})
}

func (e *ElizaSim) cancelar(st *Estado, w http.ResponseWriter, r *http.Request) {
	var b struct {
		Motivo string `json:"motivo"`
	}
	if err := ler(r, &b); err != nil {
		e.falhaL(w, 422, "VALIDATION_ERROR", "corpo inválido", nil)
		return
	}
	a := e.agendamentoDoPath(st, w, r)
	if a == nil {
		return
	}
	if !a.ativo() {
		e.falhaL(w, 409, "INVALID_TRANSITION", "este agendamento não pode ser cancelado", nil)
		return
	}
	if e.foraDaAntecedencia(a) {
		e.falhaL(w, 422, "NOTICE_TOO_SHORT", "dentro da antecedência mínima: fale com a equipe", nil)
		return
	}
	a.Status, a.Aguardando = "canceled", false
	e.okL(w, 200, map[string]any{"agendamento": st.resumo(a)})
}

func (e *ElizaSim) confirmar(st *Estado, w http.ResponseWriter, r *http.Request) {
	if err := ler(r, &struct{}{}); err != nil {
		e.falhaL(w, 422, "VALIDATION_ERROR", "corpo inválido", nil)
		return
	}
	a := e.agendamentoDoPath(st, w, r)
	if a == nil {
		return
	}
	switch a.Status {
	case "confirmed":
	case "scheduled":
		a.Status, a.Aguardando = "confirmed", false
	default:
		e.falhaL(w, 409, "INVALID_TRANSITION", "só um agendamento aprovado (scheduled) pode ser confirmado", nil)
		return
	}
	e.okL(w, 200, map[string]any{"agendamento": st.resumo(a)})
}

// --- cadastro --------------------------------------------------------------

func (st *Estado) cadastro() map[string]any {
	nome := st.nomeCadastro
	if nome == "" {
		nome = st.primeiro + " Silva"
	}
	return map[string]any{"id": "c0000000-0000-4000-8000-000000000001", "nome": nome, "email": nil,
		"documentoMascarado": "***.456.789-**", "dataNascimentoInformada": false}
}

func (e *ElizaSim) lerCadastro(st *Estado, w http.ResponseWriter, _ *http.Request) {
	if !e.exigeIdentificado(st, w) {
		return
	}
	e.okL(w, 200, map[string]any{"cadastro": st.cadastro()})
}

func soDigitos(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (e *ElizaSim) criarCadastro(st *Estado, w http.ResponseWriter, r *http.Request) {
	var b struct {
		Nome           string  `json:"nome"`
		Documento      string  `json:"documento"`
		Email          *string `json:"email"`
		DataNascimento *string `json:"dataNascimento"`
	}
	if err := ler(r, &b); err != nil || len([]rune(strings.TrimSpace(b.Nome))) < 3 {
		e.falhaL(w, 422, "VALIDATION_ERROR", "nome (mín. 3 caracteres) e documento são obrigatórios", nil)
		return
	}
	if d := soDigitos(b.Documento); len(d) != 11 && len(d) != 14 {
		e.falhaL(w, 422, "VALIDATION_ERROR", "documento deve ser CPF ou CNPJ", nil)
		return
	}
	switch st.situacao {
	case sistema.SituacaoIdentificado:
		e.falhaL(w, 409, "CUSTOMER_CONFLICT", "este número já tem cadastro", nil)
		return
	case sistema.SituacaoAmbiguo:
		e.falhaL(w, 409, "CUSTOMER_AMBIGUOUS", "mais de um cadastro neste número", nil)
		return
	}
	st.situacao, st.cadastroCriado = sistema.SituacaoIdentificado, true
	st.nomeCadastro = strings.TrimSpace(b.Nome)
	st.primeiro = strings.Fields(st.nomeCadastro)[0]
	e.okL(w, 201, map[string]any{"cadastro": st.cadastro()})
}

func (e *ElizaSim) atualizarCadastro(st *Estado, w http.ResponseWriter, r *http.Request) {
	var b struct {
		Nome           *string `json:"nome"`
		Documento      *string `json:"documento"`
		Email          *string `json:"email"`
		DataNascimento *string `json:"dataNascimento"`
	}
	if err := ler(r, &b); err != nil || (b.Nome == nil && b.Documento == nil && b.Email == nil && b.DataNascimento == nil) {
		e.falhaL(w, 422, "VALIDATION_ERROR", "informe pelo menos um campo", nil)
		return
	}
	if !e.exigeIdentificado(st, w) {
		return
	}
	if b.Nome != nil && strings.TrimSpace(*b.Nome) != "" {
		st.nomeCadastro = strings.TrimSpace(*b.Nome)
	}
	e.okL(w, 200, map[string]any{"cadastro": st.cadastro()})
}

func (e *ElizaSim) verificarNome(st *Estado, w http.ResponseWriter, r *http.Request) {
	nome := strings.Join(strings.Fields(r.URL.Query().Get("nome")), " ")
	if n := len([]rune(nome)); n < 3 || n > 120 {
		e.falhaL(w, 422, "VALIDATION_ERROR", "nome deve ter de 3 a 120 caracteres", nil)
		return
	}
	existe := false
	for _, n := range append(append([]string{}, nomesJaCadastrados...), st.cen.NomesCadastrados...) {
		if dobrar(strings.Join(strings.Fields(n), " ")) == dobrar(nome) {
			existe = true
		}
	}
	e.okL(w, 200, map[string]any{"existe": existe})
}

// --- conferência do estado final -------------------------------------------

// Conferir compara o estado do simulado com o esperado e devolve os problemas.
func (e *ElizaSim) Conferir(st *Estado, ef *EstadoFinal) []string {
	if ef == nil {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	var ruins []string
	if ef.CadastroCriado != nil && st.cadastroCriado != *ef.CadastroCriado {
		ruins = append(ruins, fmt.Sprintf("cadastro_criado=%v, esperado %v", st.cadastroCriado, *ef.CadastroCriado))
	}
	if ef.AgendamentosNovos != nil {
		n := 0
		for _, a := range st.ags {
			if a.Novo {
				n++
			}
		}
		if n != *ef.AgendamentosNovos {
			ruins = append(ruins, fmt.Sprintf("agendamentos criados=%d, esperado %d", n, *ef.AgendamentosNovos))
		}
	}
	for _, k := range ef.Agendamentos {
		achou := false
		for _, a := range st.ags {
			if (k.Ref == "" || a.Ref == k.Ref) && (k.Status == "" || a.Status == k.Status) &&
				(k.Servico == "" || a.Servico == k.Servico) && (k.Profissional == "" || a.Prof == k.Profissional) &&
				(k.InicioPrefixo == "" || strings.HasPrefix(a.Inicio, k.InicioPrefixo)) &&
				(k.Novo == nil || a.Novo == *k.Novo) {
				achou = true
			}
		}
		if !achou {
			ruins = append(ruins, "estado final sem agendamento "+descreverCond(k))
		}
	}
	return ruins
}

func descreverCond(k CondAg) string {
	var p []string
	add := func(n, v string) {
		if v != "" {
			p = append(p, n+"="+v)
		}
	}
	add("ref", k.Ref)
	add("status", k.Status)
	add("servico", k.Servico)
	add("profissional", k.Profissional)
	add("inicio", k.InicioPrefixo)
	if k.Novo != nil {
		p = append(p, fmt.Sprintf("novo=%v", *k.Novo))
	}
	return "{" + strings.Join(p, " ") + "}"
}
