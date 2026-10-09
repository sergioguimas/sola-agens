// Package openapi converte a OpenAPI de um sistema nas ferramentas do LLM
// (specs/02 §1) e baixa a spec no boot. Converter é pura (sem I/O); o subconjunto
// de OpenAPI usado é pequeno, então não há biblioteca de terceiros.
package openapi

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"sola-agens/internal/agentes"
	"sola-agens/internal/llm"
)

// Destino diz onde um argumento da ferramenta vai no request HTTP.
type Destino string

const (
	DestinoPath  Destino = "path"
	DestinoQuery Destino = "query"
	DestinoBody  Destino = "body"
)

// Ferramenta é uma operação da OpenAPI pronta para o LLM e para o conector.
type Ferramenta struct {
	Decl            llm.ToolDecl
	Metodo          string // GET, POST, ...
	Caminho         string // com {param}
	Args            map[string]Destino
	BodyObrigatorio bool // requestBody.required: sem campos, o conector manda {}
}

var nomeRe = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`)

// nomeReservado é a ferramenta interna do motor (specs/02 §4).
const nomeReservado = "chamar_humano"

var metodosHTTP = []string{"delete", "get", "head", "options", "patch", "post", "put", "trace"}

type operacao struct {
	metodo, caminho string
	no, item        map[string]any
	id              string
}

func (o operacao) atendente() bool {
	v, _ := o.no["x-atendente"].(bool)
	return v
}

type documento struct {
	raiz  map[string]any
	ops   []operacao // ordenadas por caminho e método
	porID map[string]operacao
}

// parse lê o JSON (números preservados como json.Number) e lista as operações.
func parse(spec []byte) (*documento, error) {
	dec := json.NewDecoder(bytes.NewReader(spec))
	dec.UseNumber()
	var raiz map[string]any
	if err := dec.Decode(&raiz); err != nil {
		return nil, fmt.Errorf("openapi: JSON inválido: %w", err)
	}
	paths, ok := raiz["paths"].(map[string]any)
	if !ok {
		return nil, errors.New("openapi: sem \"paths\"")
	}
	d := &documento{raiz: raiz, porID: map[string]operacao{}}
	caminhos := make([]string, 0, len(paths))
	for c := range paths {
		caminhos = append(caminhos, c)
	}
	sort.Strings(caminhos)
	for _, c := range caminhos {
		item, ok := paths[c].(map[string]any)
		if !ok {
			return nil, fmt.Errorf("openapi: path %s inválido", c)
		}
		for _, m := range metodosHTTP {
			no, ok := item[m].(map[string]any)
			if !ok {
				continue
			}
			o := operacao{metodo: strings.ToUpper(m), caminho: c, no: no, item: item}
			o.id, _ = no["operationId"].(string)
			d.ops = append(d.ops, o)
			if o.id == "" {
				continue
			}
			if outra, dup := d.porID[o.id]; dup {
				return nil, fmt.Errorf("openapi: operationId %q repetido (%s %s e %s %s)",
					o.id, outra.metodo, outra.caminho, o.metodo, o.caminho)
			}
			d.porID[o.id] = o
		}
	}
	return d, nil
}

// OperacaoDoMotor devolve método (maiúsculo) e caminho de um operationId.
func OperacaoDoMotor(spec []byte, operationID string) (metodo, caminho string, err error) {
	d, err := parse(spec)
	if err != nil {
		return "", "", err
	}
	o, ok := d.porID[operationID]
	if !ok {
		return "", "", fmt.Errorf("openapi: operação %q não existe", operationID)
	}
	return o.metodo, o.caminho, nil
}

// Converter filtra as operações com x-atendente: true e as converte. Valida o
// registro: as operações do motor (contexto, responder, escalar, midia) não
// podem ser ferramentas e todas as operações declaradas precisam existir.
func Converter(spec []byte, ag agentes.Agente) ([]Ferramenta, error) {
	d, err := parse(spec)
	if err != nil {
		return nil, err
	}
	motor := []struct {
		papel, id        string
		proibidoComoTool bool
		opcional         bool
	}{
		{"contexto", ag.Operacoes.Contexto, true, false},
		{"responder", ag.Operacoes.Responder, true, false},
		{"escalar", ag.Operacoes.Escalar, true, false},
		{"midia", ag.Operacoes.Midia, true, false},
		{"confirmar", ag.Operacoes.Confirmar, false, true},
		{"cancelar", ag.Operacoes.Cancelar, false, true},
	}
	for _, m := range motor {
		if m.id == "" {
			if m.opcional {
				continue
			}
			return nil, fmt.Errorf("openapi: operacoes.%s não definida no registro", m.papel)
		}
		o, ok := d.porID[m.id]
		if !ok {
			return nil, fmt.Errorf("openapi: operação %q (operacoes.%s) não existe na spec", m.id, m.papel)
		}
		if m.proibidoComoTool && o.atendente() {
			return nil, fmt.Errorf("openapi: operação %q (operacoes.%s) é do motor e não pode ter x-atendente", m.id, m.papel)
		}
	}

	var out []Ferramenta
	for _, o := range d.ops {
		if !o.atendente() {
			continue
		}
		f, err := d.ferramenta(o)
		if err != nil {
			return nil, err
		}
		out = append(out, f)
	}
	return out, nil
}

func (d *documento) ferramenta(o operacao) (Ferramenta, error) {
	rot := o.id
	if rot == "" {
		rot = o.metodo + " " + o.caminho
		return Ferramenta{}, fmt.Errorf("openapi: %s sem operationId", rot)
	}
	if !nomeRe.MatchString(o.id) {
		return Ferramenta{}, fmt.Errorf("openapi: operationId %q inválido (use %s)", o.id, nomeRe)
	}
	if o.id == nomeReservado {
		return Ferramenta{}, fmt.Errorf("openapi: operationId %q é reservado ao motor", o.id)
	}

	c := &conversor{raiz: d.raiz, vistos: map[string]bool{}}
	props := map[string]any{}
	args := map[string]Destino{}
	var req []string

	add := func(nome string, dest Destino, schema map[string]any, obrig bool) error {
		if ant, dup := args[nome]; dup {
			return fmt.Errorf("openapi: operação %s: campo %q colide (%s e %s)", rot, nome, ant, dest)
		}
		props[nome] = schema
		args[nome] = dest
		if obrig {
			req = append(req, nome)
		}
		return nil
	}

	params, err := c.parametros(o)
	if err != nil {
		return Ferramenta{}, fmt.Errorf("openapi: operação %s: %w", rot, err)
	}
	for _, p := range params {
		in, _ := p["in"].(string)
		if in != "path" && in != "query" { // header e cookie: injetados pelo conector
			continue
		}
		nome, _ := p["name"].(string)
		if nome == "" {
			return Ferramenta{}, fmt.Errorf("openapi: operação %s: parâmetro sem nome", rot)
		}
		sch, ok := p["schema"]
		if !ok {
			return Ferramenta{}, fmt.Errorf("openapi: operação %s: parâmetro %q sem schema", rot, nome)
		}
		desc, _ := p["description"].(string)
		s, err := c.schema(sch, desc)
		if err != nil {
			return Ferramenta{}, fmt.Errorf("openapi: operação %s: parâmetro %q: %w", rot, nome, err)
		}
		obrig, _ := p["required"].(bool)
		if err := add(nome, Destino(in), s, obrig || in == "path"); err != nil {
			return Ferramenta{}, err
		}
	}

	f := Ferramenta{Metodo: o.metodo, Caminho: o.caminho, Args: args}
	if rb, ok := o.no["requestBody"]; ok {
		rbm, err := c.objeto(rb)
		if err != nil {
			return Ferramenta{}, fmt.Errorf("openapi: operação %s: requestBody: %w", rot, err)
		}
		f.BodyObrigatorio, _ = rbm["required"].(bool)
		cont, _ := rbm["content"].(map[string]any)
		js, ok := cont["application/json"].(map[string]any)
		if !ok {
			return Ferramenta{}, fmt.Errorf("openapi: operação %s: requestBody sem application/json", rot)
		}
		bs, err := c.schema(js["schema"], "")
		if err != nil {
			return Ferramenta{}, fmt.Errorf("openapi: operação %s: body: %w", rot, err)
		}
		if t, _ := bs["type"].(string); t != "object" {
			return Ferramenta{}, fmt.Errorf("openapi: operação %s: body não é objeto", rot)
		}
		bp, _ := bs["properties"].(map[string]any)
		br, _ := bs["required"].([]string)
		obrig := map[string]bool{}
		for _, n := range br {
			obrig[n] = true
		}
		nomes := make([]string, 0, len(bp))
		for n := range bp {
			nomes = append(nomes, n)
		}
		sort.Strings(nomes)
		for _, n := range nomes {
			// body opcional: nada fica required no topo.
			if err := add(n, DestinoBody, bp[n].(map[string]any), f.BodyObrigatorio && obrig[n]); err != nil {
				return Ferramenta{}, err
			}
		}
	}

	f.Decl = llm.ToolDecl{Name: o.id, Description: descricao(o.no)}
	if len(props) > 0 { // Gemini rejeita objeto sem propriedades
		sort.Strings(req)
		pm := map[string]any{"type": "object", "properties": props}
		if len(req) > 0 {
			pm["required"] = req
		}
		f.Decl.Parameters = pm
	}
	return f, nil
}

func descricao(no map[string]any) string {
	s, _ := no["summary"].(string)
	d, _ := no["description"].(string)
	s, d = strings.TrimSpace(s), strings.TrimSpace(d)
	switch {
	case s != "" && d != "":
		return s + "\n\n" + d
	case s != "":
		return s
	}
	return d
}
