// Package agentes carrega e valida o registro de agentes (agentes/*.yaml),
// conforme specs/05 §6. Os valores das envs referenciadas (segredo, URL base,
// token) são resolvidos no carregamento e ficam em campos não exportados.
package agentes

import (
	"bytes"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"

	"go.yaml.in/yaml/v3"
)

// Modos de identidade (specs/01 §2). Só Encaminhado é suportado na F1.
const (
	ModoEncaminhado = "encaminhado"
	ModoAssercao    = "assercao"
	ModoAnonimo     = "anonimo"
)

// Políticas de mídia.
const (
	MidiaProcessar      = "processar"
	MidiaRespostaFixa   = "resposta_fixa"
	tiposMidiaValidos   = "audio imagem documento video figurinha outro"
	limiteMaxIterPadrao = 8
)

var idRe = regexp.MustCompile(`^[a-z][a-z0-9-]{1,31}$`)

// MinSegredo é o tamanho mínimo do segredo HMAC da entrada: 32 caracteres
// (128 bits em hex). O .env.example gera 64 com `openssl rand -hex 32`.
const MinSegredo = 32

// Entrada: como autenticar o que chega do sistema.
type Entrada struct {
	SegredoEnv       string `yaml:"segredo_env"`
	HeaderAssinatura string `yaml:"header_assinatura"`
	HeaderTimestamp  string `yaml:"header_timestamp"`
}

// Sistema: como chamar o sistema.
type Sistema struct {
	BaseURLEnv   string            `yaml:"base_url_env"`
	OpenAPIPath  string            `yaml:"openapi_path"`
	TokenEnv     string            `yaml:"token_env"`
	HeaderTicket string            `yaml:"header_ticket"`
	HeadersFixos map[string]string `yaml:"headers_fixos"`
}

// Operacoes: operationId das operações que o motor usa.
type Operacoes struct {
	Contexto  string `yaml:"contexto"`
	Responder string `yaml:"responder"`
	Escalar   string `yaml:"escalar"`
	Midia     string `yaml:"midia"`
	Confirmar string `yaml:"confirmar"` // fastpath; opcional
	Cancelar  string `yaml:"cancelar"`  // fastpath; opcional
}

// Limites do agente. Zero no yaml = padrão.
type Limites struct {
	MaxIter          int `yaml:"max_iter"`
	RajadaMs         int `yaml:"rajada_ms"`
	RajadaMaxMs      int `yaml:"rajada_max_ms"`
	MemoriaMensagens int `yaml:"memoria_mensagens"`
	MemoriaHoras     int `yaml:"memoria_horas"`
	AudioMaxS        int `yaml:"audio_max_s"`
	TurnoTimeoutS    int `yaml:"turno_timeout_s"`
}

// SegredoTreinoMin é o tamanho mínimo do segredo do treino (specs/06 §3).
const SegredoTreinoMin = 32

// TreinoLimites do modo de treino. Zero no yaml = padrão.
type TreinoLimites struct {
	SessoesDiaTenant int `yaml:"sessoes_dia_tenant"`
	MensagensSessao  int `yaml:"mensagens_sessao"`
	TextoMax         int `yaml:"texto_max"`
	InatividadeMin   int `yaml:"inatividade_min"`
	TurnoTimeoutS    int `yaml:"turno_timeout_s"`
}

// Treino liga o modo de treino do agente (specs/06 §3). Sem o bloco, o modo fica
// desligado e as rotas respondem TREINO_DESLIGADO.
type Treino struct {
	SegredoEnv       string        `yaml:"segredo_env"`
	HeaderAssinatura string        `yaml:"header_assinatura"`
	HeaderTimestamp  string        `yaml:"header_timestamp"`
	Papeis           []string      `yaml:"papeis"`
	Limites          TreinoLimites `yaml:"limites"`
}

// PapelPermitido diz se o papel do operador pode treinar (T2).
func (t *Treino) PapelPermitido(papel string) bool {
	return slices.Contains(t.Papeis, papel)
}

// Web liga o canal web do agente (specs/07 §2). Sem o bloco, o canal fica
// desligado. Zero no yaml = padrão.
type Web struct {
	TetoTurnosDiaOrg int `yaml:"teto_turnos_dia_org"`
	TurnoTimeoutS    int `yaml:"turno_timeout_s"`
}

func (w *Web) validar() error {
	if w.TetoTurnosDiaOrg == 0 {
		w.TetoTurnosDiaOrg = 300
	}
	if w.TurnoTimeoutS == 0 {
		w.TurnoTimeoutS = 40
	}
	if w.TetoTurnosDiaOrg < 0 || w.TurnoTimeoutS < 0 {
		return errors.New("web: limites negativos")
	}
	return nil
}

// Agente é um sistema registrado no motor.
type Agente struct {
	ID             string            `yaml:"id"`
	ModoIdentidade string            `yaml:"modo_identidade"`
	Entrada        Entrada           `yaml:"entrada"`
	Sistema        Sistema           `yaml:"sistema"`
	Operacoes      Operacoes         `yaml:"operacoes"`
	Midia          map[string]string `yaml:"midia"`
	Limites        Limites           `yaml:"limites"`
	Modelo         string            `yaml:"modelo"` // vazio = AGENS_MODEL
	Treino         *Treino           `yaml:"treino"` // nil = modo de treino desligado
	Web            *Web              `yaml:"web"`    // nil = canal web desligado

	// Valores resolvidos das envs; nunca logar.
	segredo       string
	segredoTreino string
	baseURL       string
	token         string
}

// Segredo de assinatura da entrada.
func (a *Agente) Segredo() string { return a.segredo }

// SegredoTreino assina as chamadas do modo de treino (S9: não é o da entrada).
func (a *Agente) SegredoTreino() string { return a.segredoTreino }

// BaseURL do sistema (sem barra final).
func (a *Agente) BaseURL() string { return a.baseURL }

// Token de serviço do sistema.
func (a *Agente) Token() string { return a.token }

// PoliticaMidia devolve a política do tipo de anexo (ausente = resposta_fixa).
func (a *Agente) PoliticaMidia(tipo string) string {
	if p, ok := a.Midia[tipo]; ok {
		return p
	}
	return MidiaRespostaFixa
}

// String não vaza segredos.
func (a Agente) String() string {
	return fmt.Sprintf("agente{id=%s modo=%s}", a.ID, a.ModoIdentidade)
}

// LogValue implementa slog.LogValuer sem segredos.
func (a Agente) LogValue() slog.Value {
	return slog.GroupValue(slog.String("id", a.ID), slog.String("modo", a.ModoIdentidade))
}

func (l *Limites) aplicarPadroes() {
	def := func(p *int, v int) {
		if *p == 0 {
			*p = v
		}
	}
	def(&l.MaxIter, limiteMaxIterPadrao)
	def(&l.RajadaMs, 3000)
	def(&l.RajadaMaxMs, 8000)
	def(&l.MemoriaMensagens, 20)
	def(&l.MemoriaHoras, 24)
	def(&l.AudioMaxS, 120)
	def(&l.TurnoTimeoutS, 90)
}

func (l Limites) validar() error {
	for nome, v := range map[string]int{
		"max_iter": l.MaxIter, "rajada_ms": l.RajadaMs, "rajada_max_ms": l.RajadaMaxMs,
		"memoria_mensagens": l.MemoriaMensagens, "memoria_horas": l.MemoriaHoras,
		"audio_max_s": l.AudioMaxS, "turno_timeout_s": l.TurnoTimeoutS,
	} {
		if v < 0 {
			return fmt.Errorf("limites.%s negativo", nome)
		}
	}
	if l.RajadaMaxMs < l.RajadaMs {
		return errors.New("limites.rajada_max_ms menor que rajada_ms")
	}
	return nil
}

// Carregar lê dir/*.yaml, valida e resolve as envs via lookup. Diretório sem
// arquivos devolve lista vazia (o main decide se isso é fatal). A "operações
// existem na spec" é validada depois do download da OpenAPI (fase 3).
func Carregar(dir string, lookup func(string) (string, bool)) ([]Agente, error) {
	arquivos, err := filepath.Glob(filepath.Join(dir, "*.yaml"))
	if err != nil {
		return nil, err
	}
	sort.Strings(arquivos)
	var out []Agente
	vistos := map[string]string{}
	for _, f := range arquivos {
		a, err := carregarArquivo(f, lookup)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", filepath.Base(f), err)
		}
		if outro, ok := vistos[a.ID]; ok {
			return nil, fmt.Errorf("%s: id %q já usado em %s", filepath.Base(f), a.ID, outro)
		}
		vistos[a.ID] = filepath.Base(f)
		out = append(out, *a)
	}
	return out, nil
}

func carregarArquivo(f string, lookup func(string) (string, bool)) (*Agente, error) {
	raw, err := os.ReadFile(f)
	if err != nil {
		return nil, err
	}
	var a Agente
	dec := yaml.NewDecoder(bytes.NewReader(raw))
	dec.KnownFields(true) // campo desconhecido = typo no yaml
	if err := dec.Decode(&a); err != nil {
		return nil, fmt.Errorf("yaml inválido: %w", err)
	}
	if err := a.validar(lookup); err != nil {
		return nil, err
	}
	return &a, nil
}

func (a *Agente) validar(lookup func(string) (string, bool)) error {
	if !idRe.MatchString(a.ID) {
		return fmt.Errorf("id inválido (%q): use ^[a-z][a-z0-9-]{1,31}$", a.ID)
	}
	switch a.ModoIdentidade {
	case ModoEncaminhado:
	case ModoAssercao, ModoAnonimo:
		return fmt.Errorf("modo_identidade %q não suportado nesta versão", a.ModoIdentidade)
	case "":
		return errors.New("modo_identidade é obrigatório")
	default:
		return fmt.Errorf("modo_identidade inválido (%q)", a.ModoIdentidade)
	}

	obrig := []struct{ nome, v string }{
		{"entrada.segredo_env", a.Entrada.SegredoEnv},
		{"entrada.header_assinatura", a.Entrada.HeaderAssinatura},
		{"entrada.header_timestamp", a.Entrada.HeaderTimestamp},
		{"sistema.base_url_env", a.Sistema.BaseURLEnv},
		{"sistema.openapi_path", a.Sistema.OpenAPIPath},
		{"sistema.token_env", a.Sistema.TokenEnv},
		{"sistema.header_ticket", a.Sistema.HeaderTicket},
		{"operacoes.contexto", a.Operacoes.Contexto},
		{"operacoes.responder", a.Operacoes.Responder},
		{"operacoes.escalar", a.Operacoes.Escalar},
		{"operacoes.midia", a.Operacoes.Midia},
	}
	for _, o := range obrig {
		if strings.TrimSpace(o.v) == "" {
			return fmt.Errorf("%s é obrigatório", o.nome)
		}
	}
	if !strings.HasPrefix(a.Sistema.OpenAPIPath, "/") {
		return errors.New("sistema.openapi_path deve começar com /")
	}

	env := func(nome, campo string) (string, error) {
		v, ok := lookup(nome)
		v = strings.TrimSpace(v)
		if !ok || v == "" {
			return "", fmt.Errorf("%s: env %s não definida ou vazia", campo, nome)
		}
		return v, nil
	}
	var err error
	if a.segredo, err = env(a.Entrada.SegredoEnv, "entrada.segredo_env"); err != nil {
		return err
	}
	if len(a.segredo) < MinSegredo {
		return fmt.Errorf("entrada.segredo_env: env %s curta demais (mínimo %d caracteres; gere com openssl rand -hex 32)",
			a.Entrada.SegredoEnv, MinSegredo)
	}
	if a.baseURL, err = env(a.Sistema.BaseURLEnv, "sistema.base_url_env"); err != nil {
		return err
	}
	a.baseURL = strings.TrimRight(a.baseURL, "/")
	if a.token, err = env(a.Sistema.TokenEnv, "sistema.token_env"); err != nil {
		return err
	}

	for tipo, pol := range a.Midia {
		if !strings.Contains(" "+tiposMidiaValidos+" ", " "+tipo+" ") {
			return fmt.Errorf("midia.%s: tipo desconhecido", tipo)
		}
		if pol != MidiaProcessar && pol != MidiaRespostaFixa {
			return fmt.Errorf("midia.%s: política inválida (%q): use processar ou resposta_fixa", tipo, pol)
		}
	}

	if a.Treino != nil {
		if a.segredoTreino, err = a.Treino.validar(env); err != nil {
			return err
		}
	}
	if a.Web != nil {
		if err := a.Web.validar(); err != nil {
			return err
		}
	}

	a.Limites.aplicarPadroes()
	return a.Limites.validar()
}

func (l *TreinoLimites) aplicarPadroes() {
	def := func(p *int, v int) {
		if *p == 0 {
			*p = v
		}
	}
	def(&l.SessoesDiaTenant, 10)
	def(&l.MensagensSessao, 80)
	def(&l.TextoMax, 1000)
	def(&l.InatividadeMin, 30)
	def(&l.TurnoTimeoutS, 30)
}

// validar confere o bloco e devolve o segredo resolvido.
func (t *Treino) validar(env func(nome, campo string) (string, error)) (string, error) {
	for _, o := range []struct{ nome, v string }{
		{"treino.segredo_env", t.SegredoEnv},
		{"treino.header_assinatura", t.HeaderAssinatura},
		{"treino.header_timestamp", t.HeaderTimestamp},
	} {
		if strings.TrimSpace(o.v) == "" {
			return "", fmt.Errorf("%s é obrigatório", o.nome)
		}
	}
	if len(t.Papeis) == 0 {
		return "", errors.New("treino.papeis não pode ser vazio")
	}
	segredo, err := env(t.SegredoEnv, "treino.segredo_env")
	if err != nil {
		return "", err
	}
	if len(segredo) < SegredoTreinoMin {
		return "", fmt.Errorf("treino.segredo_env: env %s precisa de ao menos %d bytes", t.SegredoEnv, SegredoTreinoMin)
	}
	t.Limites.aplicarPadroes()
	for nome, v := range map[string]int{
		"sessoes_dia_tenant": t.Limites.SessoesDiaTenant, "mensagens_sessao": t.Limites.MensagensSessao,
		"texto_max": t.Limites.TextoMax, "inatividade_min": t.Limites.InatividadeMin,
		"turno_timeout_s": t.Limites.TurnoTimeoutS,
	} {
		if v < 0 {
			return "", fmt.Errorf("treino.limites.%s negativo", nome)
		}
	}
	return segredo, nil
}
