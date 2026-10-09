// Package conector executa uma ferramenta (operação da OpenAPI) contra o sistema:
// monta o request pelo mapa de argumentos, injeta identidade e headers fixos e
// traduz o envelope de resposta (specs/02 §2). Não loga nada: quem chama decide.
package conector

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"

	"sola-agens/internal/agentes"
	"sola-agens/internal/openapi"
)

// Identidade vem do turno, nunca do LLM. F1 (encaminhado): só o ticket.
type Identidade struct{ Ticket string }

// ErroRegra é um erro de negócio do sistema que o LLM pode explicar ao cliente.
type ErroRegra struct {
	Erro     string          `json:"erro"`
	Mensagem string          `json:"mensagem"`
	Detalhes json.RawMessage `json:"detalhes,omitempty"`
}

// Resultado da execução de uma ferramenta.
type Resultado struct {
	Dados     json.RawMessage // `data` do envelope (2xx)
	ErroRegra *ErroRegra      // 4xx de regra: o loop continua
	Avisos    []string        // ex.: "campo X ignorado"
	RequestID string          // do envelope de erro ou do header X-Request-Id
	Status    int             // status HTTP da resposta (0 se nenhuma chamada saiu)
}

// ParaLLM devolve o que vai no functionResponse: o `data` (objeto; outro tipo
// vira {"resultado": …}) ou {erro, mensagem, detalhes}; mais "avisos" se houver.
func (r Resultado) ParaLLM() map[string]any {
	out := map[string]any{}
	switch {
	case r.ErroRegra != nil:
		out["erro"], out["mensagem"] = r.ErroRegra.Erro, r.ErroRegra.Mensagem
		if len(r.ErroRegra.Detalhes) > 0 && string(r.ErroRegra.Detalhes) != "null" {
			var d any
			if json.Unmarshal(r.ErroRegra.Detalhes, &d) == nil {
				out["detalhes"] = d
			}
		}
	case len(r.Dados) > 0:
		var d any
		if json.Unmarshal(r.Dados, &d) == nil {
			if m, ok := d.(map[string]any); ok {
				out = m
			} else {
				out["resultado"] = d
			}
		}
	}
	if len(r.Avisos) > 0 {
		out["avisos"] = r.Avisos
	}
	return out
}

// ErrAbortar é uma falha do motor, do sistema ou da configuração: o turno aborta
// (specs/02 §2.1). Code é o code do envelope ou REDE, TIMEOUT, NAO_ENVELOPE.
// Nunca carrega texto de mensagem do sistema.
type ErrAbortar struct {
	Code      string
	Status    int
	RequestID string
	Causa     error
}

func (e *ErrAbortar) Error() string {
	s := fmt.Sprintf("sistema: %s (status %d)", e.Code, e.Status)
	if e.RequestID != "" {
		s += " request_id=" + e.RequestID
	}
	return s
}

func (e *ErrAbortar) Unwrap() error { return e.Causa }

// Códigos que abortam o turno (specs/02 §2.1).
var codigosAbortar = map[string]bool{
	"UNAUTHORIZED": true, "TICKET_MISSING": true, "TICKET_INVALID": true, "TICKET_EXPIRED": true,
	"VERSION_MISMATCH": true, "ADDON_INACTIVE": true, "RATE_LIMITED": true, "INTERNAL_ERROR": true,
}

// Executor executa uma ferramenta; o agente depende só desta interface.
type Executor interface {
	Executar(ctx context.Context, f openapi.Ferramenta, args map[string]any, id Identidade) (Resultado, error)
}

// Config é o que o conector precisa de um agente.
type Config struct {
	BaseURL      string // sem barra final
	Token        string
	HeaderTicket string
	HeadersFixos map[string]string
}

// ConfigDe extrai a Config do registro do agente.
func ConfigDe(ag *agentes.Agente) Config {
	return Config{BaseURL: ag.BaseURL(), Token: ag.Token(),
		HeaderTicket: ag.Sistema.HeaderTicket, HeadersFixos: ag.Sistema.HeadersFixos}
}

const (
	esperaRetentativa = 500 * time.Millisecond
	limiteCorpo       = 32 << 20 // mídia: 16 MB decodificados ≈ 22 MB em base64
)

// HTTP implementa Executor sobre net/http.
type HTTP struct {
	Config  Config
	Cliente *http.Client
	Timeout time.Duration // por chamada (AGENS_SISTEMA_TIMEOUT)
	// Esperar é a pausa antes da retentativa de GET; injetável nos testes.
	Esperar func(ctx context.Context, d time.Duration) error
}

var _ Executor = (*HTTP)(nil)

// NovoHTTP cria o executor com o timeout por chamada.
func NovoHTTP(cfg Config, timeout time.Duration) *HTTP {
	return &HTTP{Config: cfg, Cliente: &http.Client{}, Timeout: timeout, Esperar: esperar}
}

func esperar(ctx context.Context, d time.Duration) error {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Executar monta e envia o request. Retenta uma vez, só em GET, após 500 ms em
// 5xx, timeout ou erro de rede; escrita nunca é retentada.
func (h *HTTP) Executar(ctx context.Context, f openapi.Ferramenta, args map[string]any, id Identidade) (Resultado, error) {
	req, avisos, regra := h.montar(f, args)
	if regra != nil {
		return Resultado{ErroRegra: regra, Avisos: avisos}, nil
	}

	tentativas := 1
	if f.Metodo == http.MethodGet {
		tentativas = 2
	}
	var ultimo error
	for i := 0; i < tentativas; i++ {
		if i > 0 {
			wait := h.Esperar
			if wait == nil {
				wait = esperar
			}
			if err := wait(ctx, esperaRetentativa); err != nil {
				return Resultado{Avisos: avisos}, &ErrAbortar{Code: "TIMEOUT", Causa: err}
			}
		}
		status, corpo, reqID, err := h.uma(ctx, f.Metodo, req, id)
		if err != nil {
			ultimo = err
			if ctx.Err() != nil { // cancelamento do turno: não adianta retentar
				break
			}
			continue
		}
		res, err := interpretar(status, corpo, reqID)
		res.Avisos = avisos
		var ab *ErrAbortar
		if errors.As(err, &ab) && status >= 500 && i+1 < tentativas {
			ultimo = err
			continue
		}
		return res, err
	}
	return Resultado{Avisos: avisos}, ultimo
}

// requisicao é o request já montado (sem identidade).
type requisicao struct {
	url   string
	corpo []byte // nil = sem corpo
}

// montar valida os argumentos e monta URL e body. regra != nil quando falta um
// argumento de path (o LLM pode corrigir; nenhuma chamada sai).
func (h *HTTP) montar(f openapi.Ferramenta, args map[string]any) (requisicao, []string, *ErroRegra) {
	nomes := make([]string, 0, len(args))
	for n := range args {
		nomes = append(nomes, n)
	}
	sort.Strings(nomes)

	var avisos []string
	caminho := f.Caminho
	query := url.Values{}
	body := map[string]any{}
	for _, n := range nomes {
		v := args[n]
		dest, ok := f.Args[n]
		if !ok {
			avisos = append(avisos, "campo "+n+" ignorado")
			continue
		}
		if v == nil { // DECISÃO: null do LLM equivale a campo omitido
			continue
		}
		switch dest {
		case openapi.DestinoPath:
			s := valorTexto(v)
			if s == "" || s == "." || s == ".." {
				return requisicao{}, avisos, &ErroRegra{Erro: "ARGUMENTO_INVALIDO", Mensagem: "valor inválido para " + n}
			}
			caminho = strings.ReplaceAll(caminho, "{"+n+"}", url.PathEscape(s))
		case openapi.DestinoQuery:
			if arr, ok := v.([]any); ok {
				for _, x := range arr {
					query.Add(n, valorTexto(x))
				}
			} else {
				query.Set(n, valorTexto(v))
			}
		case openapi.DestinoBody:
			body[n] = v
		}
	}
	// Todo {param} do caminho precisa ter sido preenchido.
	if i := strings.IndexByte(caminho, '{'); i >= 0 {
		falta := caminho[i+1:]
		if j := strings.IndexByte(falta, '}'); j >= 0 {
			falta = falta[:j]
		}
		return requisicao{}, avisos, &ErroRegra{Erro: "ARGUMENTO_AUSENTE", Mensagem: "falta o argumento obrigatório " + falta}
	}

	r := requisicao{url: h.Config.BaseURL + caminho}
	if len(query) > 0 {
		r.url += "?" + query.Encode()
	}
	if len(body) > 0 || f.BodyObrigatorio {
		r.corpo, _ = json.Marshal(body) // map[string]any vindo de JSON: não falha
	}
	return r, avisos, nil
}

// valorTexto formata escalares sem notação científica.
func valorTexto(v any) string {
	switch x := v.(type) {
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(x), 'f', -1, 32)
	case int:
		return strconv.Itoa(x)
	case int64:
		return strconv.FormatInt(x, 10)
	case json.Number:
		return x.String()
	}
	b, _ := json.Marshal(v)
	return string(b)
}

// uma faz uma chamada HTTP com o timeout por chamada.
func (h *HTTP) uma(ctx context.Context, metodo string, r requisicao, id Identidade) (status int, corpo []byte, reqID string, err error) {
	if h.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, h.Timeout)
		defer cancel()
	}
	var rd io.Reader
	if r.corpo != nil {
		rd = bytes.NewReader(r.corpo)
	}
	req, err := http.NewRequestWithContext(ctx, metodo, r.url, rd)
	if err != nil {
		return 0, nil, "", &ErrAbortar{Code: "REDE", Causa: errors.New("request inválido")}
	}
	req.Header.Set("Accept", "application/json")
	if r.corpo != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range h.Config.HeadersFixos {
		req.Header.Set(k, v)
	}
	req.Header.Set("Authorization", "Bearer "+h.Config.Token)
	if id.Ticket != "" {
		req.Header.Set(h.Config.HeaderTicket, id.Ticket)
	}

	cli := h.Cliente
	if cli == nil {
		cli = http.DefaultClient
	}
	resp, err := cli.Do(req)
	if err != nil {
		code := "REDE"
		if errors.Is(err, context.DeadlineExceeded) {
			code = "TIMEOUT"
		}
		// O erro do net/http inclui a URL; guardamos só o tipo.
		return 0, nil, "", &ErrAbortar{Code: code, Causa: errors.New("falha na chamada ao sistema")}
	}
	defer resp.Body.Close()
	corpo, err = io.ReadAll(io.LimitReader(resp.Body, limiteCorpo))
	if err != nil {
		code := "REDE"
		if errors.Is(err, context.DeadlineExceeded) {
			code = "TIMEOUT"
		}
		return resp.StatusCode, nil, resp.Header.Get("X-Request-Id"),
			&ErrAbortar{Code: code, Status: resp.StatusCode, Causa: errors.New("falha ao ler a resposta")}
	}
	return resp.StatusCode, corpo, resp.Header.Get("X-Request-Id"), nil
}

type envelope struct {
	Data  json.RawMessage `json:"data"`
	Error *struct {
		Code      string          `json:"code"`
		Message   string          `json:"message"`
		Details   json.RawMessage `json:"details"`
		RequestID string          `json:"request_id"`
	} `json:"error"`
}

// interpretar traduz status + corpo (specs/02 §2).
func interpretar(status int, corpo []byte, reqHeader string) (Resultado, error) {
	res := Resultado{Status: status, RequestID: reqHeader}
	var env envelope
	if err := json.Unmarshal(corpo, &env); err != nil {
		return res, &ErrAbortar{Code: "NAO_ENVELOPE", Status: status, RequestID: reqHeader}
	}
	if env.Error != nil && env.Error.RequestID != "" {
		res.RequestID = env.Error.RequestID
	}

	if status >= 200 && status < 300 {
		if env.Data == nil {
			return res, &ErrAbortar{Code: "NAO_ENVELOPE", Status: status, RequestID: res.RequestID}
		}
		res.Dados = env.Data
		return res, nil
	}
	if env.Error == nil || env.Error.Code == "" {
		return res, &ErrAbortar{Code: "NAO_ENVELOPE", Status: status, RequestID: res.RequestID}
	}
	if status >= 500 || status < 400 || codigosAbortar[env.Error.Code] {
		return res, &ErrAbortar{Code: env.Error.Code, Status: status, RequestID: res.RequestID}
	}
	res.ErroRegra = &ErroRegra{Erro: env.Error.Code, Mensagem: env.Error.Message}
	if len(env.Error.Details) > 0 && string(env.Error.Details) != "null" {
		res.ErroRegra.Detalhes = env.Error.Details
	}
	return res, nil
}
