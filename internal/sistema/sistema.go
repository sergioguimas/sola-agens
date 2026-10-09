// Package sistema expõe as operações do motor (contexto, responder, escalar,
// mídia) sobre o conector, tipadas para o contrato do Eliza (F1). As operações
// vêm do registro do agente e são localizadas na OpenAPI (specs/02 §2, 04 §2).
package sistema

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"

	"sola-agens/internal/agentes"
	"sola-agens/internal/conector"
	"sola-agens/internal/openapi"
)

// Sistema são as quatro operações que o motor chama por conta própria.
type Sistema interface {
	Contexto(ctx context.Context, id conector.Identidade) (Contexto, error)
	// Responder devolve o mensagemId do envio (vazio se o sistema mandou null).
	Responder(ctx context.Context, id conector.Identidade, texto string) (mensagemID string, err error)
	Escalar(ctx context.Context, id conector.Identidade, motivo, resumo string) (equipeNotificada bool, err error)
	BaixarMidia(ctx context.Context, id conector.Identidade, mensagemID string) (Midia, error)
}

// Códigos de erro de regra que o motor distingue nas operações do motor.
const (
	CodNotFound      = "NOT_FOUND"
	CodMediaTooLarge = "MEDIA_TOO_LARGE"
)

// Sentinelas para errors.Is(err, ...) (mapeados pelo Code do ErrSistema).
var (
	ErrNotFound      = errors.New("sistema: NOT_FOUND")
	ErrMediaTooLarge = errors.New("sistema: MEDIA_TOO_LARGE")
)

// ErrSistema é um erro de regra devolvido por uma operação do motor. Nas
// ferramentas ele vira resposta ao LLM; aqui o motor decide pelo Code.
// Error() não inclui a mensagem do sistema.
type ErrSistema struct {
	Code     string
	Mensagem string
	Status   int
}

func (e *ErrSistema) Error() string {
	return fmt.Sprintf("sistema: %s (status %d)", e.Code, e.Status)
}

// Is liga as sentinelas ao Code.
func (e *ErrSistema) Is(alvo error) bool {
	switch alvo {
	case ErrNotFound:
		return e.Code == CodNotFound
	case ErrMediaTooLarge:
		return e.Code == CodMediaTooLarge
	}
	return false
}

// Midia baixada do sistema (já decodificada).
type Midia struct {
	Mime  string
	Bytes []byte
}

// operacao é uma operação do motor já localizada na spec.
type operacao struct{ f openapi.Ferramenta }

// Real implementa Sistema sobre um conector.Executor.
type Real struct {
	exec                                conector.Executor
	contexto, responder, escalar, midia operacao
}

var _ Sistema = (*Real)(nil)

// Novo localiza as operações do motor (registro) na spec. Método e caminho vêm
// da OpenAPI; os nomes dos argumentos são o contrato do Eliza (F1).
func Novo(exec conector.Executor, spec []byte, ops agentes.Operacoes) (*Real, error) {
	r := &Real{exec: exec}
	montar := func(opID string, args map[string]openapi.Destino, body bool) (operacao, error) {
		m, c, err := openapi.OperacaoDoMotor(spec, opID)
		if err != nil {
			return operacao{}, err
		}
		return operacao{openapi.Ferramenta{Metodo: m, Caminho: c, Args: args, BodyObrigatorio: body}}, nil
	}
	var err error
	if r.contexto, err = montar(ops.Contexto, nil, false); err != nil {
		return nil, err
	}
	if r.responder, err = montar(ops.Responder, map[string]openapi.Destino{"texto": openapi.DestinoBody}, true); err != nil {
		return nil, err
	}
	if r.escalar, err = montar(ops.Escalar, map[string]openapi.Destino{"motivo": openapi.DestinoBody, "resumo": openapi.DestinoBody}, true); err != nil {
		return nil, err
	}
	if r.midia, err = montar(ops.Midia, map[string]openapi.Destino{"mensagemId": openapi.DestinoPath}, false); err != nil {
		return nil, err
	}
	return r, nil
}

// chamar executa e converte erro de regra em *ErrSistema.
func (r *Real) chamar(ctx context.Context, op operacao, args map[string]any, id conector.Identidade) (json.RawMessage, error) {
	res, err := r.exec.Executar(ctx, op.f, args, id)
	if err != nil {
		return nil, err
	}
	if res.ErroRegra != nil {
		return nil, &ErrSistema{Code: res.ErroRegra.Erro, Mensagem: res.ErroRegra.Mensagem, Status: res.Status}
	}
	return res.Dados, nil
}

func (r *Real) Contexto(ctx context.Context, id conector.Identidade) (Contexto, error) {
	d, err := r.chamar(ctx, r.contexto, nil, id)
	if err != nil {
		return Contexto{}, err
	}
	var c Contexto
	if err := json.Unmarshal(d, &c); err != nil {
		return Contexto{}, errors.New("sistema: contexto com formato inesperado")
	}
	return c, nil
}

func (r *Real) Responder(ctx context.Context, id conector.Identidade, texto string) (string, error) {
	d, err := r.chamar(ctx, r.responder, map[string]any{"texto": texto}, id)
	if err != nil {
		return "", err
	}
	var out struct {
		MensagemID *string `json:"mensagemId"`
	}
	if err := json.Unmarshal(d, &out); err != nil {
		return "", errors.New("sistema: resposta de enviarMensagem com formato inesperado")
	}
	if out.MensagemID == nil {
		return "", nil
	}
	return *out.MensagemID, nil
}

func (r *Real) Escalar(ctx context.Context, id conector.Identidade, motivo, resumo string) (bool, error) {
	d, err := r.chamar(ctx, r.escalar, map[string]any{"motivo": motivo, "resumo": resumo}, id)
	if err != nil {
		return false, err
	}
	var out struct {
		EquipeNotificada bool `json:"equipeNotificada"`
	}
	if err := json.Unmarshal(d, &out); err != nil {
		return false, errors.New("sistema: resposta de escalar com formato inesperado")
	}
	return out.EquipeNotificada, nil
}

func (r *Real) BaixarMidia(ctx context.Context, id conector.Identidade, mensagemID string) (Midia, error) {
	d, err := r.chamar(ctx, r.midia, map[string]any{"mensagemId": mensagemID}, id)
	if err != nil {
		return Midia{}, err
	}
	var out struct {
		Mime   string `json:"mime"`
		Base64 string `json:"base64"`
	}
	if err := json.Unmarshal(d, &out); err != nil {
		return Midia{}, errors.New("sistema: resposta de mídia com formato inesperado")
	}
	b, err := base64.StdEncoding.DecodeString(out.Base64)
	if err != nil {
		return Midia{}, errors.New("sistema: mídia com base64 inválido")
	}
	return Midia{Mime: out.Mime, Bytes: b}, nil
}
