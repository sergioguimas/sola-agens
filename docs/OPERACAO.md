# Operação

Como configurar, subir, ligar a um sistema e acompanhar o agens em produção. O README traz só a visão
geral; o que é específico do ambiente da Sola está aqui.

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
[specs/05 §6](../specs/05-arquitetura-go.md#6-registro-do-agente). Campo desconhecido no yaml derruba o boot.

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

Na VPS da Sola o agens fica em `/opt/stacks/solasoftware/sola-agens` e entra na **`proxy_net`**, a rede
que todo app da VPS já usa (A10): sistema novo não precisa de rede extra para falar com ele. O
[docker-compose.yml](../docker-compose.yml) do repositório já é o de produção. Ele **não tem labels do
Traefik nem `ports:`**, então nada é público (A7); quando houver rota pública (F2b), ela entra como label
só nessa rota.

```
cd /opt/stacks/solasoftware/sola-agens
sudo git -c safe.directory='*' pull
sudo docker compose up -d --build
sudo docker logs -f sola-agens     # precisa baixar a spec de cada sistema no boot
```

## Ligar ao Eliza

Os segredos precisam ser **o mesmo valor** dos dois lados:

| No Eliza | Aqui (`.env` do agens) |
|---|---|
| `ATENDENTE_URL=http://sola-agens:4100/a/eliza` | — (é o nome do serviço acima) |
| `ATENDENTE_ENCAMINHAMENTO_SECRET` | `ELIZA_ENCAMINHAMENTO_SECRET` |
| `AUTOATENDIMENTO_API_TOKEN` | `ELIZA_AUTOATENDIMENTO_API_TOKEN` |
| — | `ELIZA_BASE_URL` = nome do serviço do Eliza na rede Docker + porta |

Na VPS o container do Eliza se chama **`elisa-app`** (com "s"), porta 3000: `ELIZA_BASE_URL=http://elisa-app:3000`.
Preencha `ATENDENTE_URL` no Eliza **por último**, com o agens já saudável: é ela que liga o encaminhamento
(vazia, o webhook do Eliza segue como antes).

A instância da Evolution de cada org com o add-on precisa assinar os eventos **`MESSAGES_UPSERT` e
`SEND_MESSAGE`**. Sem o segundo, o que o Eliza envia pela API (respostas do agens, lembretes,
notificações) não volta ao agens: não há eco das próprias respostas nem aviso do sistema como contexto
(contrato 09 do Eliza, passo 0 verificado em 27/10/2026).

Ordem de ligação: suba o agens; confira `GET /health` e, no log, `ferramentas carregadas` (12 do Eliza);
no Eliza, `autoatendimento_config.ativo=true` **primeiro só na org de teste `admin`**; faça o
[aceite manual](../CHECKLIST-ACEITE.md); só então outra org.

## Logs

JSON no stdout (`docker logs sola-agens`). Cada turno termina em uma linha `"msg":"turno"`:

```
docker logs sola-agens | jq -c 'select(.msg=="turno")'
```

Campos fixos: `agente`, `tenant`, `conversa` (= `sha256(telefone)[:12]`), `turno` (uuid). No `turno`:
`modo` (llm, fastpath, fixa, pausado, erro), `resultado`, `iteracoes`, `ferramentas` (quantidade),
`tokens_entrada`, `tokens_saida`, `custo_micro_usd`, `latencia_ms`, `escalou`. Outros eventos úteis:
`eco`, `aviso` (com `tipo`), `pausa` (com `ate`), `duplicado`, `entrada rejeitada` (com `motivo`),
`fila cheia`, `eco_sem_id`, `sistema_abortou` (com `request_id` do Eliza), `manutenção`.

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

## Avaliação antes do aceite

Rode `go run ./avaliacao` **antes do aceite manual e a cada troca de modelo ou prompt** (detalhes no
[README](../README.md#avaliação-do-modelo-e-do-prompt)). Usa a chave paga e custa centavos por bateria.
Os cenários do [CHECKLIST-ACEITE](../CHECKLIST-ACEITE.md) que dependem de infraestrutura ficam para o
aceite manual: 16–18 (pausa e eco), 19–21 (áudio e foto), 22 (reentrega) e 24 (agens fora).

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
página de preços do Gemini antes de confiar no total. O preço promocional do `gemini-3.6-flash` até
31/12/2026 (A9) entra por `AGENS_PRECO_GEMINI_3_6_FLASH`; sem a linha, o custo registrado é o cheio (o
dobro). Com a linha, **apague-a em janeiro de 2027**.

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
