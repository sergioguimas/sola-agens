# Auditoria de segurança — 28/10/2026

Escopo: todo o código Go (`main.go`, `health.go`, `internal/**`, `avaliacao/**`),
`go.mod`/`go.sum`, `Dockerfile`, `docker-compose.yml`, `.env.example` e `agentes/*.yaml`.

Ferramentas e fontes:

- `govulncheck` (golang.org/x/vuln) com o banco oficial `golang.org/x/vulndb`
  na revisão de 27/10/2026 (4.712 entradas OSV), com análise de alcance (só conta
  vulnerabilidade que o código de fato chama).
- `go list -m -u all` para versões desatualizadas; `go vet ./...`;
  `go test -race ./...` (tudo passa).
- Context7 (docs atuais de Go e modernc.org/sqlite) e busca por CVEs da SQLite
  embutida.
- Revisão manual da superfície de ataque: entrada HTTP, autenticação, rota admin,
  conector (chamadas que o LLM dispara), clientes Gemini/Groq, SQLite, logs e imagem.

## Resumo

| # | Severidade | Achado |
|---|-----------|--------|
| 1 | **Alta** | Toolchain Go 1.26.0: 28 vulnerabilidades da biblioteca padrão alcançáveis pelo código |
| 2 | Média | Imagens base sem versão fixa (`golang:1.26`, `distroless/static-debian12`) |
| 3 | Média | Ticket do cliente reenviado em redirect para outro host (conector) |
| 4 | Baixa | Segredo HMAC e `AGENS_ADMIN_TOKEN` sem tamanho mínimo |
| 5 | Baixa | Dependências com versões novas disponíveis (sem CVE conhecido) |
| 6 | Baixa | Leituras sem limite das respostas Gemini/Groq |
| 7 | Info | Mime do áudio vai cru para o cabeçalho multipart do Groq |
| 8 | Info | HTTP sem TLS entre containers (aceito pelo desenho A7) |

**Dependências de terceiros: nenhum CVE conhecido.** Os 47 registros que o
govulncheck acha (28 alcançáveis + 19 só importados/requeridos) são todos da
biblioteca padrão do Go 1.26.0. Com Go **1.26.10** o mesmo scan dá
`No vulnerabilities found`.

## 1. Go 1.26.0 com 28 vulnerabilidades alcançáveis (Alta)

`go.mod` declara `go 1.26.0` e não há diretiva `toolchain`; quem compila
localmente com 1.26.0 gera um binário vulnerável. A versão de correção mais
recente da linha é **1.26.10** (a 1.27.3 também existe).

Alcançáveis a partir do código (trechos de chamada achados pelo govulncheck),
agrupados:

- **net/http (servidor e cliente)** — GO-2026-6617 (crash por corrida no HPACK
  do HTTP/2), GO-2026-6613 e GO-2026-6605 (dessincronização HTTP/1 após CONNECT),
  GO-2026-6612, GO-2026-6611, GO-2026-6610, GO-2026-6603 (memória/CPU no HTTP/2),
  GO-2026-6089 (ReadHeaderTimeout não aplicado no h2c), GO-2026-5026 (punycode),
  GO-2026-4918 (loop infinito no transporte HTTP/2). Todas corrigidas até 1.26.9.
  Alcance: `main.go:157` (`ListenAndServe`), conector, Gemini, Groq.
- **crypto/tls** — GO-2026-6607 (ECH), GO-2026-6090 (mensagens pós-handshake),
  GO-2026-5856 (vazamento de ECH), GO-2026-4870 (DoS por KeyUpdate).
- **crypto/x509** — GO-2026-6809 (distrust de intermediários comprometidos,
  corrigida só na **1.26.10**), GO-2026-5037, GO-2026-4947, GO-2026-4946,
  GO-2026-4866 (bypass de name constraints), GO-2026-4600, GO-2026-4599.
- **net/url** — GO-2026-6218 (complexidade quadrática), GO-2026-4601 (IPv6).
- **net/textproto / mime/multipart** — GO-2026-6608 (limite de memória burlado),
  GO-2026-5039.
- **encoding/asn1** GO-2026-5972; **os** GO-2026-4602; **net** GO-2026-4971 (só Windows).

O impacto mais direto é DoS no servidor `:4100` e nas chamadas de saída
(Gemini, Groq, Eliza). Como o servidor só escuta na rede Docker interna (A7),
o risco real depende de quem mais está na `proxy_net`.

**Observação sobre produção:** o `Dockerfile` usa `FROM golang:1.26`, tag
flutuante. Se a imagem foi construída recentemente, provavelmente usou 1.26.10;
se foi construída antes e só reiniciada, carrega o Go da época. Para conferir,
extraia o binário (`docker cp sola-agens:/sola-agens .`) e rode
`go version sola-agens` (o `-s -w` não remove essa informação).

**Correção:**

```
# go.mod
go 1.26.0
toolchain go1.26.10
```

e no `Dockerfile` `FROM golang:1.26.10` (de preferência com digest, ver item 2).
Reconstruir e reimplantar a imagem.

## 2. Imagens base sem versão fixa (Média)

- `golang:1.26` muda a cada patch: build não reprodutível e sem garantia de
  ter as correções do item 1.
- `gcr.io/distroless/static-debian12:nonroot` — Debian 12 (bookworm) já tem
  sucessor; a distroless publica `static-debian13`. A imagem static só traz
  ca-certificates, tzdata e netbase, então a superfície é pequena, mas o pacote
  de CAs e o tzdata envelhecem.

**Correção:** fixar por digest (`FROM golang:1.26.10@sha256:…`,
`FROM gcr.io/distroless/static-debian13:nonroot@sha256:…`) e atualizar
periodicamente (Dependabot/Renovate cobrem Dockerfile e go.mod).

## 3. Ticket reenviado em redirect para outro host (Média)

`internal/conector/conector.go:135` cria `&http.Client{}` sem `CheckRedirect`.
Ao seguir um 3xx para outro domínio, o `net/http` remove `Authorization`,
`Cookie` e afins, **mas copia os demais cabeçalhos**, incluindo o
`X-Autoatendimento-Ticket` (o ticket que age em nome do cliente final) e os
`headers_fixos`. Basta um redirect aberto ou mal configurado no Eliza para o
ticket vazar para terceiros. Uma API JSON de serviço não deveria redirecionar.

Os clientes de `main.go:71` (download da OpenAPI) e do Gemini/Groq têm o mesmo
padrão; lá só vai `Authorization`/`x-goog-api-key`. O `x-goog-api-key` também
não é cabeçalho sensível para o `net/http` e seria reenviado.

**Correção:** não seguir redirects em nenhum cliente de saída:

```go
&http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
	return http.ErrUseLastResponse
}}
```

O conector já trata status fora de 2xx como `NAO_ENVELOPE`, então o 3xx vira
abortar o turno, que é o comportamento certo.

## 4. Segredos sem tamanho mínimo (Baixa)

`internal/agentes/agentes.go` só exige que a env do segredo HMAC não esteja
vazia, e `AGENS_ADMIN_TOKEN` aceita qualquer valor não vazio. Um segredo
curto (ex.: `teste`) passa no boot. O `.env.example` recomenda
`openssl rand -hex 32`, mas nada impõe.

**Correção:** recusar no boot segredo/token com menos de 32 bytes.

Pontos positivos verificados nessa área: HMAC-SHA256 com `hmac.Equal`
(tempo constante), janela de ±300 s, dedupe por `mensagemId` impedindo replay
dentro da janela (eventos retidos 7 dias), token admin comparado por SHA-256 +
`subtle.ConstantTimeCompare`, rota admin desligada quando o token está vazio,
segredos fora de `String()`/`LogValue()`.

## 5. Dependências desatualizadas (Baixa)

Nenhuma tem CVE conhecido no banco do Go. Atualizações disponíveis:

| Módulo | Atual | Disponível |
|---|---|---|
| modernc.org/sqlite (direta) | v1.59.0 (SQLite 3.53.4) | v1.61.0 (SQLite 3.54.1) |
| golang.org/x/text (direta) | v0.42.0 | v0.43.0 |
| golang.org/x/sys | v0.47.0 | v0.49.0 |
| modernc.org/libc | v1.75.7 | v1.79.1 |
| github.com/dustin/go-humanize | v1.0.1 | v1.1.0 |
| github.com/ncruces/go-strftime | v1.0.0 | v1.1.0 |

`gopkg.in/yaml.v3` v3.0.1, `github.com/google/uuid` v1.6.0 e
`github.com/joho/godotenv` v1.5.1 estão na última versão. O `yaml.v3` está
arquivado pelo autor original (sem manutenção); a continuação mantida é
`go.yaml.in/yaml/v3`. Como só lê arquivos locais confiáveis (`agentes/*.yaml`),
o risco é baixo, mas vale migrar.

SQLite embutida: o govulncheck não cobre o código C transpilado. A 3.53.4 já
inclui as correções de CVE-2026-11822 e CVE-2026-11824 (FTS5, corrigidas na
3.53.2); o projeto não usa FTS5 nem a extensão Session (CVE-2026-50812).

**Correção:** `go get -u ./... && go mod tidy`, rodar a suíte.

## 6. Leituras sem limite das respostas Gemini/Groq (Baixa)

`internal/llm/gemini/client.go:73` e `internal/transcricao/groq.go:110` usam
`io.ReadAll(resp.Body)` sem limite. O conector (`limiteCorpo`) e o download da
OpenAPI (`maxSpec`) já limitam. São endpoints confiáveis via TLS, então o
risco é baixo, mas um `io.LimitReader` (ex.: 8 MB) deixa tudo consistente.

## 7. Mime cru no multipart do Groq (Info)

`internal/transcricao/groq.go:178` coloca o mime vindo do sistema no cabeçalho
da parte multipart sem validação; `textproto` não filtra `\r\n`. A origem é
assinada (HMAC) ou vem do próprio sistema, e `limparMime` corta em `;`, então
não é explorável hoje. Validar com `mime.ParseMediaType` e exigir `audio/*`
fecharia a porta.

## 8. HTTP sem TLS entre containers (Info)

O servidor escuta HTTP puro e o `ELIZA_BASE_URL` é `http://elisa-app:3000`.
Aceito pelo desenho (A7: só rede Docker interna, sem Traefik). O corpo é
autenticado por HMAC, mas trafega em claro com telefone e texto do cliente, e o
token de serviço e o ticket também. Se outros apps da `proxy_net` forem pouco
confiáveis, uma rede Docker dedicada entre agens e Eliza reduz a exposição.

## O que foi verificado e está correto

- **SQL injection:** todas as consultas em `internal/store/sqlite` usam
  parâmetros `?`; o DSN é montado só com constantes e o caminho de dados.
- **Path traversal no conector:** argumentos de path passam por
  `url.PathEscape` e `.`/`..` são recusados; só os campos da OpenAPI com
  `x-atendente` viram ferramentas; operações do motor não podem ser chamadas pelo LLM.
- **Identidade:** o ticket vem do turno, nunca dos argumentos do LLM;
  cabeçalhos e cookies da OpenAPI não viram argumentos.
- **Limites:** corpo de entrada 64 KB (`MaxBytesReader`), `ReadHeaderTimeout`,
  `ReadTimeout` e `WriteTimeout` no servidor, áudio até 16 MB, spec até 16 MB,
  máx. de iterações do agente, fila por conversa limitada.
- **Logs:** contato só como hash; texto, áudio e argumentos de ferramenta
  ficam fora do log, salvo `AGENS_LOG_DEBUG=true` (documentado como pontual).
- **Container:** distroless sem shell, usuário não-root (65532), `.env` fora da
  imagem (`.dockerignore`), sem portas publicadas.
- **Concorrência:** `go test -race ./...` passa.

## Ordem sugerida

1. Item 1: `toolchain go1.26.10` + Dockerfile fixo, rebuild e deploy.
2. Item 3: `CheckRedirect` nos clientes de saída.
3. Itens 2, 4 e 5 juntos numa rodada de manutenção.
4. Itens 6 e 7 quando mexer nesses arquivos.
