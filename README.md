# Sola Agens — atendente da Sola

**Status:** em construção (09/10/2026). Todas as
decisões da F1 fechadas em 09/10 ([CONTRATO §10](CONTRATO.md#10-decisões-da-f1)). Build da F1 concluído (fases 1–6): atende texto e áudio, imagem Docker de 23 MB,
`-race` limpo no Linux, bateria de avaliação em `avaliacao/`. Próximo passo: rodar a avaliação com a chave do Gemini
e o aceite manual ([CHECKLIST-ACEITE](CHECKLIST-ACEITE.md)) contra o Eliza de dev. PR do Eliza codado (contrato 07, aceite pendente).

Plataforma interna da Sola para pôr um atendente (LLM com ferramentas) em qualquer sistema da Sola. O
motor lê a **OpenAPI** do sistema e transforma as operações liberadas em ferramentas. Primeiro agente:
**Eliza** (F1). Depois: site da Sola, Axios Calc, SolaBridge.

*Agens*, do latim: "o que age".

| Doc | Para quê |
|---|---|
| [docs/DECISOES.md](docs/DECISOES.md) | por que existe e as decisões de origem (P1–P6) |
| [CONTRATO.md](CONTRATO.md) | **comece aqui** — o que o serviço faz, segurança, decisões abertas |
| [specs/](specs/) | domínio e schema · ferramentas e prompt · fluxos · integrações · arquitetura Go |
| [PLANO-DE-BUILD.md](PLANO-DE-BUILD.md) | fases, prompts por fase (Sonnet), checklist de revisão (Opus) |
| [CHECKLIST-ACEITE.md](CHECKLIST-ACEITE.md) | bateria de conversas e medições |
| [.env.example](.env.example) | variáveis |

Estrutura, padrão de documentação e boa parte do código vêm do Smaug
(`redmilab/servicos/smaug`) — ver [specs/05 §2](specs/05-arquitetura-go.md#2-mapa-de-reaproveitamento-do-smaug).

## Configurar

**`.env`**: copie `.env.example` para `.env` (fora do git; em produção as variáveis vêm do `env_file` do
container). Obrigatórias para subir:

| Variável | Para quê |
|---|---|
| `GEMINI_API_KEY` | chave do Gemini, **plano pago** (o gratuito pode usar o conteúdo) |
| `ELIZA_BASE_URL` | URL do Eliza na rede Docker (ver [Ligar ao Eliza](#ligar-ao-eliza)) |
| `ELIZA_ENCAMINHAMENTO_SECRET` | segredo da assinatura do que o Eliza encaminha (`openssl rand -hex 32`) |
| `ELIZA_AUTOATENDIMENTO_API_TOKEN` | token de serviço do Eliza |

Todo o resto tem padrão. `AGENS_ADMIN_TOKEN` vazio desliga a rota de apagamento (veja
[Retenção e apagamento](#retenção-e-apagamento)). Sem as obrigatórias o processo não sobe e diz qual falta.

**`agentes/*.yaml`**: um arquivo por sistema atendido (hoje só `eliza.yaml`): segredo e token por nome de
env, onde está a OpenAPI, quais operações o motor usa, política de mídia e limites. Formato e campos em
[specs/05 §6](specs/05-arquitetura-go.md#6-registro-do-agente). Campo desconhecido no yaml derruba o boot.

## Rodar local

```
go run .                                          # lê ./.env, escuta em AGENS_LISTEN
CGO_ENABLED=0 go build -o bin/sola-agens . && ./bin/sola-agens
curl http://127.0.0.1:4100/health                 # 200
```

O boot baixa a OpenAPI do Eliza (`ELIZA_BASE_URL`), então o Eliza precisa estar no ar. Agente, spec ou
operação inválidos derrubam o processo antes de o HTTP subir.

## Container

```
docker build -t sola-agens .
```

Imagem multi-stage: binário estático (`CGO_ENABLED=0`) sobre `distroless/static` como `nonroot`
(uid/gid 65532). `agentes/` vai **dentro** da imagem (`/agentes`): mudou o yaml, reconstrua a imagem, ou
monte `./agentes:/agentes:ro` e mande SIGHUP. O SQLite fica em `/data` (`VOLUME`). O `HEALTHCHECK` roda
`sola-agens health` (a imagem não tem shell nem curl): um GET em `127.0.0.1:<porta de AGENS_LISTEN>/health`.

- **Volume nomeado:** herda o dono (65532) da imagem; nada a fazer.
- **Bind mount** (`./dados:/data`): o diretório precisa ser do nonroot, senão o SQLite não abre:
  `sudo chown 65532:65532 ./dados`.
- **`.env` de produção:** `env_file` vence o `ENV` da imagem. Apague (ou ajuste para `/data` e
  `/agentes`) as linhas `AGENS_DATA_DIR=./data` e `AGENS_AGENTES_DIR=./agentes` do `.env.example`.

Na VPS da Sola o agens entra **na rede Docker do Eliza** e não publica porta nenhuma (só o Eliza o
alcança, A7). Exemplo de serviço (ajuste o nome da rede com `docker network ls`):

```yaml
services:
  sola-agens:
    build: ./sola-agens          # ou image: <registro>/sola-agens:<tag>
    container_name: sola-agens
    restart: unless-stopped
    env_file: .env
    expose: ["4100"]             # só documenta; NADA de ports:
    volumes:
      - agens-data:/data
    networks: [eliza]

networks:
  eliza:
    external: true
    name: <rede-do-compose-do-eliza>

volumes:
  agens-data:
```

## Ligar ao Eliza

Os segredos precisam ser **o mesmo valor** dos dois lados:

| No Eliza | Aqui (`.env` do agens) |
|---|---|
| `ATENDENTE_URL=http://sola-agens:4100/a/eliza` | — (é o nome do serviço acima) |
| `ATENDENTE_ENCAMINHAMENTO_SECRET` | `ELIZA_ENCAMINHAMENTO_SECRET` |
| `AUTOATENDIMENTO_API_TOKEN` | `ELIZA_AUTOATENDIMENTO_API_TOKEN` |
| — | `ELIZA_BASE_URL` = nome do serviço do Eliza na rede Docker + porta |

O valor de `ELIZA_BASE_URL` no `.env.example` (`http://eliza:3000`) é **palpite**: confirme o nome e a porta
no compose do Eliza na VPS (`docker compose ps`, `docker network inspect <rede>`).

Ordem de ligação: suba o agens; confira `GET /health` e, no log, `ferramentas carregadas` (12 do Eliza);
no Eliza, `autoatendimento_config.ativo=true` **primeiro só na org de teste `admin`**; faça o
[aceite manual](CHECKLIST-ACEITE.md); só então outra org.

## Logs

JSON no stdout (`docker logs sola-agens`). Cada turno termina em uma linha `"msg":"turno"`:

```
docker logs sola-agens | jq -c 'select(.msg=="turno")'
```

Campos fixos: `agente`, `tenant`, `conversa` (= `sha256(telefone)[:12]`), `turno` (uuid). No `turno`:
`modo` (llm, fastpath, fixa, pausado, erro), `resultado`, `iteracoes`, `ferramentas` (quantidade),
`tokens_entrada`, `tokens_saida`, `custo_micro_usd`, `latencia_ms`, `escalou`. Outros eventos úteis:
`duplicado`, `entrada rejeitada` (com `motivo`), `fila cheia`, `eco_sem_id`, `sistema_abortou` (com
`request_id` do Eliza), `manutenção`.

**Nunca aparece:** telefone, texto de mensagem, transcrição de áudio, argumento ou resultado de
ferramenta, ticket, token, segredo. `AGENS_LOG_DEBUG=true` é a exceção: libera argumentos e resultados
das ferramentas (que podem ter nome e documento de cliente). Use só para investigar um caso, por pouco
tempo, e apague o log depois.

## Recarga

`SIGHUP` recarrega `agentes/*.yaml` e baixa as OpenAPIs de novo, sem derrubar conversas em andamento.
Se algo estiver inválido, o conjunto anterior continua valendo e o erro vai para o log. Só Linux (no
Windows o sinal não existe). Variáveis de ambiente (tokens, chave) só mudam com reinício.

```
docker kill -s HUP sola-agens
```

## Testes

```
go vet ./...
go test ./...
```

Sem rede e sem chave: LLM, sistema e transcrição são fakes. O detector de corrida exige cgo (gcc), que o
Windows de desenvolvimento não tem; rode no container:

```
docker run --rm -v "$PWD":/src -w /src golang:1.26 go test -race ./...
```

## Avaliação do modelo e do prompt

`avaliacao/` testa o **modelo e o prompt** (não a infraestrutura): conversas do
[CHECKLIST-ACEITE](CHECKLIST-ACEITE.md) contra um **Eliza simulado em processo** (spec real, estado por
cenário, catálogo de 2 serviços e 2 profissionais, horários determinísticos) e o **Gemini real**. Não toca
no Eliza de verdade nem manda WhatsApp. Rode **antes do aceite manual e a cada troca de modelo ou prompt**.

```
GEMINI_API_KEY=... go run ./avaliacao                  # todos os cenários
go run ./avaliacao -cenario 7                          # um só
go run ./avaliacao -vezes 5                            # estabilidade: cada cenário 5 vezes
go run ./avaliacao -modelo gemini-3.8-flash            # compara outro modelo (padrão: AGENS_MODEL)
go run ./avaliacao -v                                  # mostra também as respostas ao cliente
```

Usa a chave paga e custa centavos por bateria. Imprime, por cenário: passou/falhou, o motivo, ferramentas
chamadas, tokens, custo em US$ e latência real; no fim, totais e custo médio por cenário. Sai com código 1
se qualquer cenário falhar. Sem `GEMINI_API_KEY` ele para com uma mensagem dizendo isso (código 2).

Cenários em [`avaliacao/cenarios.yaml`](avaliacao/cenarios.yaml): situação do cliente, agendamentos
existentes, rodadas de mensagens e expectativas (ferramentas chamadas e proibidas, escalonamento,
fastpath, trechos que a resposta deve ou não conter, estado final). Incluídos: 1–15 e 23 do checklist. Ficam
para o aceite manual: 16–18 (pausa e eco), 19–21 (áudio e foto), 22 (reentrega) e 24 (agens fora).

## Custos

Cada turno grava uma linha em `turnos` (sem texto). `custo_micro_usd / 1e6` = US$; `criado_em` é ms UTC.
O `sqlite3` não está na imagem; consulte por um container descartável sobre o volume (com o mesmo usuário
da imagem, para não deixar arquivos de root no volume) ou copie o banco com `docker cp`:

```
docker run --rm -it --user 65532:65532 -v <volume>:/data keinos/sqlite3 sqlite3 /data/agens.db
```

Custo e tokens por agente, tenant e dia (hora de Brasília):

```sql
SELECT agente, tenant,
       date(criado_em/1000, 'unixepoch', '-3 hours') AS dia,
       count(*)                                      AS turnos,
       sum(tokens_entrada)                           AS tokens_ent,
       sum(tokens_saida)                             AS tokens_sai,
       round(sum(custo_micro_usd) / 1e6, 4)          AS usd
FROM turnos
GROUP BY agente, tenant, dia
ORDER BY dia DESC, usd DESC;
```

Por modo e resultado, nos últimos 7 dias:

```sql
SELECT modo, resultado,
       count(*)                             AS turnos,
       round(avg(latencia_ms))              AS latencia_ms,
       sum(tokens_entrada)                  AS tokens_ent,
       sum(tokens_saida)                    AS tokens_sai,
       round(sum(custo_micro_usd) / 1e6, 4) AS usd,
       sum(escalou)                         AS escalados
FROM turnos
WHERE criado_em >= (strftime('%s', 'now', '-7 days') * 1000)
GROUP BY modo, resultado
ORDER BY turnos DESC;
```

Custo médio de um turno que usou o LLM (base para estimar o custo por cliente):

```sql
SELECT round(avg(custo_micro_usd) / 1e6, 5) AS usd_por_turno_llm
FROM turnos WHERE modo = 'llm' AND resultado = 'ok';
```

Os preços por modelo estão em `internal/llm/preco.go` e se sobrescrevem com `AGENS_PRECO_*`; confira na
página de preços do Gemini antes de confiar no total.

## Retenção e apagamento

Um job diário (hora em `AGENS_MANUTENCAO_HORA`, fuso `AGENS_TZ`; recupera até 3 dias perdidos) apaga o
**texto** das mensagens com mais de `AGENS_RETENCAO_DIAS` (90) e os eventos de dedupe e ecos com mais de 7
dias. Os turnos (auditoria e custo, sem texto) ficam.

Apagamento a pedido do titular (LGPD): apaga conversa e mensagens do contato e deixa os turnos só com o
contato em hash. Idempotente (204 também quando não havia nada); exige `AGENS_ADMIN_TOKEN`.

```
curl -X DELETE -H "Authorization: Bearer $AGENS_ADMIN_TOKEN" \
  http://sola-agens:4100/admin/agentes/eliza/tenants/<org>/contatos/<telefone>
```

`<telefone>` é o contato como o Eliza o envia (só dígitos com DDI, ex. `5511999990000`). A rota só
existe dentro da rede Docker.

## Origem do código

`internal/llm` e `internal/transcricao` foram copiados do Smaug (`redmilab/servicos/smaug`; `internal/llm`
no commit `b81d795`) e evoluem separados; o restante é código novo no molde do Smaug (specs/05 §2).

## Decisões durante o build

**Fase 1 (09/10/2026, commit c69da42)**

- Tipos `Mensagem`/`Anexo` em `internal/mensagem` (sem ciclo entre `entrada` e `agent`); `Anexo.TipoOriginal` guarda o tipo do canal quando vira `outro`.
- `AGENS_LISTEN` aceita `0.0.0.0` (diferente do Smaug): o agens roda em container, e a exposição é controlada pela rede Docker (A7).
- `Agente` guarda segredo, URL base e token em campos não exportados; `String()`/`LogValue()` mostram só id e modo.
- Registro lido com `KnownFields(true)`: campo desconhecido no yaml é erro. `operacoes.confirmar`/`cancelar` opcionais (só o fastpath usa); as quatro do motor obrigatórias. Limite `0` = padrão; negativo ou `rajada_max_ms < rajada_ms` = erro.
- `AGENS_PRECO_*` lido como mapa bruto em `Config.Precos`; o parse fica para a fase 3 (`llm/preco.go`).
- Erro do handler que não seja `ErrFilaCheia` → 500. Dedupe entra pela `Server.Dedupe` (fase 2).
- Teste de fumaça: binário real, `/health` ok, POST assinado → 202, assinatura adulterada → 401, log sem telefone/texto/segredo.

**Fase 2 (09/10/2026)**

- Dedupe por interface local `Eventos` no `httpapi`. **Qualquer** falha de entrega (não só fila cheia) desmarca o evento, para o reenvio do Eliza não virar "duplicado" e a mensagem se perder.
- SQLite com conexão única (`SetMaxOpenConns(1)`) e `_txlock=immediate`, como no Smaug. Banco em `AGENS_DATA_DIR/agens.db`.
- `Pausar` e `GuardarConfig` criam a conversa se não existir. Chave vazia, papel ou modo inválidos são erro.
- Anexos gravados como JSON sem `Ref`; `ExpurgarTexto` zera só `conteudo` (anexos não têm dado sensível sem `Ref`).
- Rota admin confere o token antes do agente (token errado = 401 mesmo com agente desconhecido).
- Funções de hash do contato em `internal/mensagem` (revisão Opus: `store` não importa `entrada`).
- Manutenção: catch-up de hoje, ontem e anteontem; no primeiro boot roda os três dias (vazios).
- Teste de fumaça: base criada, mensagem repetida → 202 com log "duplicado", apagamento com token errado → 401, certo → 204.

**Fase 3 (09/10/2026)**

- `internal/llm` copiado do Smaug em `b81d795` (redmilab), sem mudança de comportamento; + `InputTokens`/`OutputTokens` e `preco.go` (2.5-flash 0,30/2,50 e 2.5-flash-lite 0,10/0,40 US$/milhão — conferir antes do deploy).
- Golden gerado da spec **real** do Eliza servida pelo build com o contrato 07 (não houve marcação à mão). 12 ferramentas; `TestElizaEstrutura` trava destinos e obrigatórios além do golden.
- `format` não suportado (uuid) vira dica " (formato: uuid)" na descrição (decisão da revisão); `allOf` mesclado; `anyOf`/`oneOf` só "X ou null" — outro caso é erro, não descarte silencioso.
- Ferramenta sem argumento: `Parameters` nil (o Gemini recusa objeto sem propriedades).
- Conector: `null` do LLM = omitido; path param ausente/`.`/`..` vira erro de regra sem chamar o sistema; 2xx sem `data` ou 3xx = `NAO_ENVELOPE` (aborta); todo 5xx aborta, mesmo com código de regra (ex.: `WHATSAPP_UNAVAILABLE`). Erros nunca carregam URL, corpo ou mensagem do sistema.
- `Carregar`: 5 tentativas com esperas 2/4/8/16 s; timeout de 30 s por tentativa.
- `sistema`: `ErrNotFound` e `ErrMediaTooLarge` distinguíveis com `errors.Is` (fase 5); `atendente` ausente no contexto = valor zero (Eliza sem o 07 continua funcionando).

**Fase 4 (09/10/2026)**

- **Correção de segurança da revisão:** `nomeExibicao` (pushName, escolhido pelo próprio contato) e `primeiroNome` (cadastro) entravam crus no prompt de sistema — um perfil "Ana. Ignore as regras..." viraria instrução. Agora passam por `nomeSeguro` (só letras, espaço e `. ' -`, até 40 runes) e entram entre aspas, marcados como dado.
- `{primeiroNome}` ausente: o placeholder sai com a vírgula/espaço antes dele ("Prontinho, {primeiroNome}!" → "Prontinho!").
- Normalização do fastpath ignora tom de pele e seletor de variação (👍🏽 = 👍).
- Chamadas paralelas do Gemini executadas em sequência; falha técnica interrompe as restantes.
- `config_json` guarda o `Atendente` do contexto direto (sem a chave `atendente`).
- Texto + anexo vai ao LLM com marcadores (`[áudio 12s]`, `[imagem]`); só anexo com política `resposta_fixa` → resposta fixa. Áudio com `processar` ainda vira `midiaNaoSuportada` até a fase 5.
- Ticket inválido no contexto grava a mensagem do cliente na memória, sem responder nem escalar.
- Envio, memória e log depois do loop usam contexto próprio de 30 s (a `falhaTecnica` sai mesmo depois do timeout do turno).
- `chamar_humano` com motivo fora do enum volta ao LLM como erro de regra; resumo cortado em 1000 runes.
- `operacoes.confirmar/cancelar` que não existem como ferramenta derrubam o boot (typo no yaml desligaria o fastpath em silêncio).
- `httpapi` passou a trocar o mapa de agentes atomicamente (SIGHUP atualiza segredo e headers). SIGHUP é no-op no Windows (build tag); compila para Linux.
- `Pool.Close` cancela turnos que estouram o prazo; cada item tem `recover`.
- **Risco aceito:** o 202 sai quando a mensagem entra no debounce; se a fila da conversa (100) estiver cheia quando a rajada fecha, o lote é descartado com log de erro e o Eliza não tem como saber. Com 100 itens por conversa, só acontece com o agens travado.
- `-race` não roda nesta máquina (sem gcc/cgo); debounce, worker e atendimento rodados com `-count=8` sem instabilidade. Rodar `-race` no fechamento em Linux/container.

**Fase 5 (09/10/2026)**

- `internal/transcricao` copiado do Smaug (gemini sobre `llm.Provider`, groq multipart, fake). A interface devolve também `Uso` (tokens), somado ao turno; modelo do Groq sem env (padrão `whisper-large-v3-turbo`). Idioma `pt`.
- O ticket viaja em `mensagem.Mensagem.Ticket` (um por mensagem, preenchido em `atendimento.Servico.Mensagem`); `baixarMidia` usa o ticket do próprio áudio. O contexto continua usando o mais recente.
- F5 roda antes do contexto (passo 12): áudio longo, tamanho > 16 MB, `NOT_FOUND` (`audioNaoChegou`, texto só do motor), `MEDIA_TOO_LARGE` e bytes > 16 MB terminam o turno em `modo=fixa` sem LLM, mesmo que a rajada tenha texto. Falha da transcrição (ou texto vazio): `falhaTecnica`, `modo=fixa`, `resultado=erro_transcricao`, sem escalar. Outro erro de download: F2; ticket vencido no download: `ticket_invalido`.
- O LLM vê a transcrição como texto digitado; a memória grava `[áudio Ns] <texto>`. Fastpath vale para um único áudio transcrito.
- Sem `Transcritor` no Motor, política `processar` continua valendo como `resposta_fixa`.

- **Revisão Opus:** falha de transcrição respondia `falhaTecnica` ("já avisei a equipe") sem escalar — agora usa `audioFalhou` ("Não consegui entender esse áudio. Pode me escrever?"). Download de mídia ganhou executor próprio com timeout de no mínimo 30 s (o Eliza espera até 15 s pela Evolution; os 10 s do `AGENS_SISTEMA_TIMEOUT` cortariam antes). Custo da transcrição calculado com o modelo que transcreveu (`Uso.Modelo`), não com o do agente; teste novo.

**Fase 6 (09/10/2026)**

- `sola-agens health` (subcomando, não flag): lê só `AGENS_LISTEN` do ambiente, faz GET em `127.0.0.1:<porta>/health` e sai 0/1, sem `.env` nem config. É o `HEALTHCHECK` da imagem distroless.
- Dockerfile: `/data` é criado no estágio de build e copiado com `--chown=65532:65532` (a imagem final não tem shell). `env_file` sobrescreve o `ENV` da imagem: o `.env` de produção não deve repetir `AGENS_DATA_DIR`/`AGENS_AGENTES_DIR` do exemplo (documentado em Container).
- `go test -race ./...` rodado no container `golang:1.26`, limpo (o Windows de desenvolvimento não tem cgo).
- Avaliação (`avaliacao/`): usa a `atendimento.Fabrica` e o `agent.Motor` reais, com o Eliza simulado em `httptest` servindo a spec real (golden do contrato 07) e o Gemini real; sem HTTP de entrada nem debounce (cada rodada é um `agent.Turno`). Um ticket por cenário liga ao estado do simulado; cada repetição usa uma conversa nova num SQLite temporário. O relógio é fixo no mundo (qua 14/10/2026 10:00) e avança 1 s por leitura, para a memória manter ordem e janela. A latência reportada é a real; a do turno gravado vem desse relógio.
- O simulado é estrito como o Eliza: corpo com campo desconhecido → 422, horário fora da grade ou ocupado → `SLOT_UNAVAILABLE` com sugestões, antecedência, janela e limite de ativos validados. Não replica a fila de aprovação nem a UI.
- Cenários 16–18, 19–21, 22 e 24 ficam fora da avaliação (infraestrutura, transcrição real ou sem dependência do modelo); seguem no aceite manual.
- **Revisão Opus:** `gofmt` aplicado em `internal/agent/run.go`; `-race` rodado no container `golang:1.26` sobre as fases 1–5 (limpo); imagem construída e executada com um Eliza falso no host — boot completo (12 ferramentas), base gravada como não-root no volume, `HEALTHCHECK` → healthy, 23 MB. Cenário 25 na avaliação: pushName malicioso não vira instrução (cobre a correção da fase 4).
