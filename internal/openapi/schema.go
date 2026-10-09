package openapi

import (
	"errors"
	"fmt"
	"sort"
	"strings"
)

// conversor carrega a raiz da spec (para $ref) e a pilha de refs em resolução
// (proteção contra ciclo).
type conversor struct {
	raiz   map[string]any
	vistos map[string]bool
}

// resolver segue uma ref interna (#/components/...).
func (c *conversor) resolver(ref string) (map[string]any, error) {
	if !strings.HasPrefix(ref, "#/") {
		return nil, fmt.Errorf("$ref externa não suportada: %s", ref)
	}
	var atual any = c.raiz
	for _, seg := range strings.Split(ref[2:], "/") {
		seg = strings.ReplaceAll(strings.ReplaceAll(seg, "~1", "/"), "~0", "~")
		m, ok := atual.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("$ref não resolve: %s", ref)
		}
		if atual, ok = m[seg]; !ok {
			return nil, fmt.Errorf("$ref não resolve: %s", ref)
		}
	}
	m, ok := atual.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("$ref não aponta para um objeto: %s", ref)
	}
	return m, nil
}

// objeto devolve o nó como mapa, resolvendo $ref (parâmetros, requestBody).
func (c *conversor) objeto(n any) (map[string]any, error) {
	m, ok := n.(map[string]any)
	if !ok {
		return nil, errors.New("esperava um objeto")
	}
	if ref, ok := m["$ref"].(string); ok {
		return c.resolver(ref)
	}
	return m, nil
}

// parametros junta os parâmetros do path item e da operação; os da operação
// substituem os do path item com o mesmo (name, in).
func (c *conversor) parametros(o operacao) ([]map[string]any, error) {
	var out []map[string]any
	idx := map[string]int{}
	for _, lista := range []any{o.item["parameters"], o.no["parameters"]} {
		arr, _ := lista.([]any)
		for _, raw := range arr {
			p, err := c.objeto(raw)
			if err != nil {
				return nil, fmt.Errorf("parâmetro: %w", err)
			}
			nome, _ := p["name"].(string)
			in, _ := p["in"].(string)
			k := in + ":" + nome
			if i, ok := idx[k]; ok {
				out[i] = p
				continue
			}
			idx[k] = len(out)
			out = append(out, p)
		}
	}
	return out, nil
}

// tipoDe lê "type" (string ou lista 3.1 com "null").
func tipoDe(v any) (tipo string, nullable bool, err error) {
	switch t := v.(type) {
	case nil:
		return "", false, nil
	case string:
		return t, false, nil
	case []any:
		for _, x := range t {
			s, _ := x.(string)
			switch {
			case s == "null":
				nullable = true
			case tipo == "":
				tipo = s
			default:
				return "", false, errors.New("type com mais de um tipo não nulo")
			}
		}
		if tipo == "" {
			return "", false, errors.New("type só com null")
		}
		return tipo, nullable, nil
	}
	return "", false, errors.New("type inválido")
}

func ehNulo(n any) bool {
	m, ok := n.(map[string]any)
	if !ok {
		return false
	}
	t, _ := m["type"].(string)
	return t == "null"
}

// mesclar une as partes de allOf num único schema (propriedades e required
// somados; para o resto, o primeiro valor vence).
func (c *conversor) mesclar(m map[string]any) (map[string]any, error) {
	partes, ok := m["allOf"].([]any)
	if !ok {
		return m, nil
	}
	out := map[string]any{}
	for k, v := range m {
		if k != "allOf" {
			out[k] = v
		}
	}
	for _, p := range partes {
		pm, ok := p.(map[string]any)
		if !ok {
			return nil, errors.New("allOf com item inválido")
		}
		var err error
		if ref, ok := pm["$ref"].(string); ok {
			if c.vistos[ref] {
				return nil, fmt.Errorf("ciclo em $ref: %s", ref)
			}
			alvo, err2 := c.resolver(ref)
			if err2 != nil {
				return nil, err2
			}
			c.vistos[ref] = true
			pm, err = c.mesclar(alvo)
			delete(c.vistos, ref)
		} else {
			pm, err = c.mesclar(pm)
		}
		if err != nil {
			return nil, err
		}
		for k, v := range pm {
			switch k {
			case "properties":
				dst, _ := out["properties"].(map[string]any)
				if dst == nil {
					dst = map[string]any{}
				}
				src, _ := v.(map[string]any)
				for pk, pv := range src {
					dst[pk] = pv
				}
				out["properties"] = dst
			case "required":
				dst, _ := out["required"].([]any)
				src, _ := v.([]any)
				out["required"] = append(dst, src...)
			default:
				if _, tem := out[k]; !tem {
					out[k] = v
				}
			}
		}
	}
	return out, nil
}

// formatosMantidos são os únicos "format" que o Gemini aceita no schema.
var formatosMantidos = map[string]bool{"date-time": true, "date": true}

// schema converte um schema OpenAPI/JSON Schema no subconjunto do Gemini.
// desc, se não vazio, substitui a description (usado por parâmetros e por $ref
// com description ao lado).
func (c *conversor) schema(n any, desc string) (map[string]any, error) {
	m, ok := n.(map[string]any)
	if !ok {
		return nil, errors.New("schema inválido")
	}
	if ref, ok := m["$ref"].(string); ok {
		if c.vistos[ref] {
			return nil, fmt.Errorf("ciclo em $ref: %s", ref)
		}
		alvo, err := c.resolver(ref)
		if err != nil {
			return nil, err
		}
		if s, _ := m["description"].(string); s != "" {
			desc = s
		}
		c.vistos[ref] = true
		defer delete(c.vistos, ref)
		return c.schema(alvo, desc)
	}
	m, err := c.mesclar(m)
	if err != nil {
		return nil, err
	}

	// anyOf/oneOf: só o padrão "X ou null" (ou uma variante única).
	for _, k := range []string{"anyOf", "oneOf"} {
		vars, ok := m[k].([]any)
		if !ok || m["type"] != nil {
			continue
		}
		var resto []any
		nulo := false
		for _, v := range vars {
			if ehNulo(v) {
				nulo = true
			} else {
				resto = append(resto, v)
			}
		}
		if len(resto) != 1 {
			return nil, fmt.Errorf("%s com %d variantes não suportado", k, len(resto))
		}
		s, err := c.schema(resto[0], desc)
		if err != nil {
			return nil, err
		}
		if nulo {
			s["nullable"] = true
		}
		return s, nil
	}

	out := map[string]any{}
	tipo, nullable, err := tipoDe(m["type"])
	if err != nil {
		return nil, err
	}
	_, temProps := m["properties"].(map[string]any)
	if tipo == "" {
		switch {
		case temProps:
			tipo = "object"
		case m["items"] != nil:
			tipo = "array"
		}
	}
	if tipo != "" {
		out["type"] = tipo
	}
	if nb, _ := m["nullable"].(bool); nullable || nb {
		out["nullable"] = true
	}

	if e, ok := m["enum"].([]any); ok {
		out["enum"] = e
	} else if cv, ok := m["const"]; ok {
		out["enum"] = []any{cv}
	}
	for _, k := range []string{"minimum", "maximum", "minItems", "maxItems"} {
		if v, ok := m[k]; ok {
			out[k] = v
		}
	}

	// description + dica do format descartado (o LLM ainda sabe que é um uuid).
	d := desc
	if d == "" {
		d, _ = m["description"].(string)
	}
	if f, _ := m["format"].(string); f != "" {
		switch {
		case formatosMantidos[f]:
			out["format"] = f
		case d != "":
			d += " (formato: " + f + ")"
		default:
			d = "Formato: " + f + "."
		}
	}
	if d != "" {
		out["description"] = d
	}

	if props, ok := m["properties"].(map[string]any); ok && len(props) > 0 {
		nomes := make([]string, 0, len(props))
		for k := range props {
			nomes = append(nomes, k)
		}
		sort.Strings(nomes)
		po := make(map[string]any, len(props))
		for _, k := range nomes {
			s, err := c.schema(props[k], "")
			if err != nil {
				return nil, fmt.Errorf("propriedade %q: %w", k, err)
			}
			po[k] = s
		}
		out["properties"] = po
		var req []string
		seen := map[string]bool{}
		if rs, ok := m["required"].([]any); ok {
			for _, r := range rs {
				if s, _ := r.(string); s != "" && po[s] != nil && !seen[s] {
					seen[s] = true
					req = append(req, s)
				}
			}
		}
		if len(req) > 0 {
			out["required"] = req
		}
	}
	if it, ok := m["items"]; ok {
		s, err := c.schema(it, "")
		if err != nil {
			return nil, fmt.Errorf("items: %w", err)
		}
		out["items"] = s
	}
	return out, nil
}
