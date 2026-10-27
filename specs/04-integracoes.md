# 04 — Integrações

Contratos externos do agens. O lado do Eliza está contratado em
`Eliza/docs/contratos/autoatendimento/` (README, 02 e 06) e implementado (F0); a §6 lista o que o
Eliza ainda precisa mudar para a F1.

---

## 1. Entrada encaminhada (Eliza → agens)

- **Rota:** `POST /a/{agente}/v1/mensagens`. O Eliza monta a URL como `{ATENDENTE_URL}/v1/mensagens`,
  então no Eliza `ATENDENTE_URL=http://sola-agens:4100/a/eliza`. O `{agente}` escolhe **qual segredo**
  valida a assinatura; um sistema não consegue falar em nome de outro sem o segredo dele.
- **Corpo:** `MensagemEncaminhada` (Zod em `Eliza/web/contracts/autoatendimento/mensagens.ts`).
  Struct Go espelhada em `internal/entrada/encaminhado.go`; campos desconhecidos são ignorados (o
  Eliza pode acrescentar campo sem quebrar o agens).
- **Assinatura:** `X-Eliza-Assinatura = hex(HMAC-SHA256(segredo, X-Eliza-Timestamp + "." + corpo
  bruto))`, `X-Eliza-Timestamp` em epoch segundos, tolerância 300 s. Nomes dos headers vêm do
  registro (`entrada.header_assinatura`, `entrada.header_timestamp`).
- **Resposta:** `202 {"aceito":true}` em até 3 s (o Eliza aborta em 3 s e cai no fluxo antigo). O
  processamento é sempre assíncrono.
- **Ticket:** `ticket.token` é opaco para o agens; vale 30 min; a conversa guarda em memória o mais
  recente (não vai para a base — é credencial de curta duração).

## 2. Chamadas ao sistema (Eliza)

Headers em toda chamada (ferramenta ou operação do motor):

```http
Authorization: Bearer <ELIZA_AUTOATENDIMENTO_API_TOKEN>
X-Autoatendimento-Ticket: <ticket mais recente da conversa>
X-Autoatendimento-Versao: 1
Content-Type: application/json            (quando há body)
```

Envelope: `{ "data": …, "meta": … }` ou `{ "error": { code, message, details, request_id } }`.
Status por código em `HTTP_STATUS_BY_CODE` do Eliza. Tratamento: 02 §2. `request_id` do erro vai
para o log do turno (ajuda a cruzar com o log do Eliza).

| Operação do motor | Rota | Uso |
|---|---|---|
| `obterContexto` | `GET /api/v1/autoatendimento/contexto` | todo turno (F1 passo 13) |
| `enviarMensagem` | `POST /api/v1/autoatendimento/mensagens` `{texto ≤ 4000}` | resposta; devolve `{mensagemId}` para o eco |
| `escalarParaEquipe` | `POST /api/v1/autoatendimento/escalonamentos` `{motivo, resumo}` | F4 |
| `baixarMidia` | `GET /api/v1/autoatendimento/mensagens/{mensagemId}/midia` (**novo**, §6) | F5 |

Limites do lado do Eliza (`aa-escrita` 20/h, `aa-criar` 5/dia, `aa-msg-contato` 60/h,
`aa-escalar` 3/h) contêm um agens em loop; o agens não replica esses limites.

Tempo: `AGENS_SISTEMA_TIMEOUT` (10 s) por chamada; `GET` com 1 retentativa; escrita nunca.

## 3. Gemini

Copiado do Smaug (`redmilab/servicos/smaug/specs/04 §2` e `internal/llm`): REST
`generateContent` sem SDK, `x-goog-api-key`, `functionDeclarations`, chamadas paralelas,
`thinkingConfig.thinkingBudget = 0`, `temperature = 0`, retry em 429/5xx respeitando `Retry-After`,
interface `llm.Provider` com `fake` roteirizado.

- **Modelo:** `gemini-3.6-flash` (A9, revê a A2), sobrescrevível por agente.
- **Plano pago obrigatório** (o gratuito pode usar o conteúdo). A chave é da conta Sola com
  faturamento ativo.
- **Tokens:** `usageMetadata.promptTokenCount` e `candidatesTokenCount` de cada chamada, somados no
  turno.
- **Custo estimado:** tabela de preço por modelo em `internal/llm/preco.go` (US$ por milhão de
  tokens, entrada/saída), sobrescrevível por `AGENS_PRECO_<MODELO>`. Valores iniciais conferidos na
  página de preços do Gemini antes do build; o log é estimativa, a fatura do Google é a verdade.

## 4. Transcrição

Copiada do Smaug (`specs/04 §2.6`, `internal/transcricao`): interface `Transcritor`, provedores
`gemini` (sobre o mesmo `llm.Provider`, sem tools) e `groq` (`whisper-large-v3-turbo`, multipart).
**Padrão `AGENS_TRANSCRICAO=gemini`**: o áudio do cliente final fica no mesmo provedor pago já
aceito para LGPD. `groq` só se o Sérgio decidir depois (outro operador de dado). Idioma `pt`.

## 5. Rede e deploy

Container `sola-agens` na rede Docker do Eliza, na VPS da Sola (A7): o Eliza chama
`http://sola-agens:4100`, o agens chama o Eliza pelo nome do serviço interno (`ELIZA_BASE_URL`).
Sem rota no Traefik na F1. Saída para a internet só para o Gemini (e o Groq, se ligado).

## 6. O que o Eliza precisa mudar

Um PR no Eliza. **Contrato escrito:** `Eliza/docs/contratos/autoatendimento/07-ajustes-sola-agens.md`
(09/10) — ele manda na forma; a tabela abaixo é o resumo do pedido. Ordem: os itens 1–3 bloqueiam a fase 6 (aceite); o 4 bloqueia só o aceite de
áudio; o 5 é necessário para a pausa e as respostas personalizadas.

| # | Mudança | Onde | Por quê |
|---|---|---|---|
| 1 | `x-atendente: true` nas operações da 02 §3 (e em nenhuma operação do motor) | `web/lib/openapi/autoatendimento.ts` | A6 |
| 2 | `GET /cadastro/nome?nome=` → `{ existe: boolean }`: busca normalizada (sem acento, minúsculas, espaços colapsados) por nome completo **exato** na org do ticket; não devolve id nem dado; limite próprio (`aa-nome`, 10/h) | rota + `lib/domain` + Zod + OpenAPI (`verificarNomeCadastrado`, `x-atendente: true`) | B3 |
| 3 | Motivos `numero_novo` e `falha_tecnica` no enum do `EscalonarBody` (o texto ao humano mostra o motivo) | `contracts/autoatendimento/mensagens.ts` | B3, F2 |
| 4 | **Áudio:** `conteudo` ganha `{ tipo: "audio", duracaoS, mime, tamanho, encaminhado }` (hoje vira `nao_suportado`); rota `GET /mensagens/{mensagemId}/midia` que só aceita o `mensagemId` **do próprio ticket** (`ticket.msg`), baixa da Evolution (`getBase64FromMediaMessage`) e devolve `{ mime, tamanho, base64 }` no envelope; acima de 16 MB, `MEDIA_TOO_LARGE` | `encaminhar.ts`, `mensagens.ts`, rota nova, `gateway.ts` | A8 — o agens não tem a chave da Evolution (C3 do Eliza) |
| 5 | Campo `atendente` no `Contexto`: `{ persona, tom, pausaHumanaHoras, respostas: {…}, palavras: { confirmar[], cancelar[] } }`, todos anuláveis, já combinados (padrão do Eliza + org); colunas tipadas novas em `autoatendimento_config` (persona, tom, horas de pausa, uma coluna por resposta, `text[]` para as palavras — sem jsonb, C12 do 07) | migration + `contexto.ts` + 01-configuracao | B2, M1, M3, M4 |

Decididos no 07: colunas tipadas em vez de jsonb (C12), limite `aa-nome` 10/h e busca só para
`desconhecido` (C9), versão do contrato continua `1` (C8).
