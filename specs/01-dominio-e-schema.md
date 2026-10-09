# 01 — Domínio e schema

O que o agens guarda e as regras sobre esses dados. Decisões de origem: CONTRATO §10 (A1, A4, A8,
B1, B2, M1). Ferramentas e prompt em [02](02-ferramentas-e-prompt.md), fluxos em [03](03-fluxos.md).

---

## 1. Chaves: agente, tenant, contato (A1)

| Chave | O que é | Eliza (F1) | Site (F2) |
|---|---|---|---|
| `agente` | um sistema registrado no motor | `eliza` | `site` |
| `tenant` | a conta do cliente dentro do sistema | `organizacao.id` da mensagem encaminhada | fixo `sola` |
| `contato` | a outra ponta da conversa | `contato.telefone` (só dígitos, como veio) | id de sessão do widget |

- **Conversa = `agente + tenant + contato`.** É a chave da fila, da memória, da pausa e do log.
- **Nenhuma leitura atravessa tenant.** Toda função do `Store` recebe as três chaves (ou
  `agente + tenant` para operações de tenant); não existe "listar todas as conversas" fora da
  manutenção (§8) e do apagamento (§9).
- O `tenant` vem **da entrada autenticada** (corpo assinado pelo sistema), nunca do LLM.

## 2. Agente (registro no motor)

O motor guarda só **como achar e autenticar o sistema** (A4). Arquivo `agentes/<id>.yaml`, lido no
boot e validado (boot falha se inválido). Formato em [05 §6](05-arquitetura-go.md#6-registro-do-agente).

| Campo | Para quê |
|---|---|
| `id` | chave `agente`; também é o segmento da rota de entrada (`/a/{id}/v1/mensagens`) |
| `modo_identidade` | `encaminhado` (F1) · `assercao` (F3) · `anonimo` (F2) |
| `entrada` | nome da env do segredo de assinatura e nomes dos headers de assinatura/timestamp |
| `sistema` | env da URL base, caminho da OpenAPI, env do token de serviço, headers fixos, header do ticket |
| `operacoes` | `operationId` das operações que o **motor** chama por conta própria: `contexto`, `responder`, `escalar`, `midia` (nunca ferramentas) e `confirmar`, `cancelar` (fastpath; também são ferramentas) |
| `midia` | política por tipo de anexo (§5) |
| `limites` | iterações, rajada, janela de memória, limites de áudio, timeout do turno |
| `modelo` | opcional; padrão `AGENS_MODEL` |

Persona, tom, instruções, respostas padrão e horas de pausa **não** ficam aqui: vêm do sistema a
cada turno (§3).

## 3. Configuração em camadas (A4, B2, M4)

```
padrão do motor (Go, fallback)  ◀── sobrescrito por ──  config combinada que o sistema entrega
                                                         (sistema + tenant, já mesclada pelo sistema)
```

- **O sistema entrega a configuração já combinada** no contexto do turno. No Eliza: campo
  `atendente` do `GET /contexto` (pedido ao Eliza em [04 §6](04-integracoes.md#6-o-que-o-eliza-precisa-mudar))
  mais o `politica.instrucoesAtendimento` e o `organizacao.termos` que já existem.
- O motor aplica campo a campo: o que vier nulo ou ausente cai no padrão do motor.
- **A regra de segurança do prompt e a lista de ferramentas nunca vêm de configuração.**
- A última configuração combinada recebida é guardada na conversa (`conversas.config_json`) para
  quem precisa dela sem contexto do turno: a pausa por `deMim`, que chega sem ticket (§4).

Campos (forma do lado do motor; o Eliza segue o Zod que o PR dele definir):

| Campo | Padrão do motor | Uso |
|---|---|---|
| `persona` | `null` (o agente não se apresenta por nome) | nome do atendente no prompt |
| `tom` | "cordial, direto, frases curtas" | seção PERSONA do prompt |
| `instrucoes` | `null` | Eliza: `politica.instrucoesAtendimento` |
| `pausaHumanaHoras` | `AGENS_PAUSA_HUMANA_HORAS` (12) | §4 |
| `respostas.*` | dicionário do motor (02 §5) | respostas fixas e do fastpath |
| `palavras.confirmar`, `palavras.cancelar` | dicionário do motor (02 §5) | fastpath |

## 4. Conversa e pausa (M1)

Estado da conversa: **ativa** ou **pausada até um instante**.

- Mensagem `deMim` que **não** é eco do próprio agens (§6) → `pausada_ate = agora + pausaHumanaHoras`.
  Cada nova `deMim` reinicia o prazo. `pausaHumanaHoras` sai de `conversas.config_json` (última
  configuração vista) ou, sem ela, do padrão do motor.
- Enquanto pausada, mensagem do cliente **não** gera resposta, nem fastpath. Ela é gravada na memória
  (o agens precisa do contexto quando voltar) e o turno é registrado com modo `pausado`.
- Volta sozinha quando `pausada_ate` passa. Não há comando de retomada na F1 (a equipe que quiser
  devolver antes muda a configuração de pausa; comando fica no backlog).

## 5. Mensagem e mídia (A8)

Mensagem de entrada normalizada pelo modo de entrada:

```go
type Mensagem struct {
    ID         string    // id no canal (WhatsApp: mensagemId)
    RecebidaEm time.Time
    Texto      string    // vazio quando só há anexo
    Anexos     []Anexo
    NomeExibicao string  // pushName; só conveniência, nunca identidade (B3)
}
type Anexo struct {
    Tipo      string // audio | imagem | documento | video | figurinha | outro
    Mime      string
    Tamanho   int64  // bytes; 0 se o canal não informou
    DuracaoS  int    // só áudio/vídeo
    Encaminhado bool
    Ref       string // como pedir o arquivo ao canal/sistema; o agens não guarda o arquivo
}
```

Política por agente, por tipo: `processar` ou `resposta_fixa`. F1 (Eliza): `audio: processar`, todo o
resto `resposta_fixa` (`respostas.midiaNaoSuportada`). Ligar imagem depois = mudar a política e
escrever o processador; o schema não muda. **Nenhum arquivo é gravado em disco ou base**: áudio é
baixado em memória, transcrito e descartado.

## 6. Ecos

Toda resposta enviada pelo agens guarda o `mensagemId` que o sistema devolveu (Eliza:
`EnviarMensagemResposta.mensagemId`). Quando esse id volta como `deMim`, é eco: ignora, sem pausar.
Se o sistema devolveu `mensagemId: null`, o eco não é reconhecível e **vai pausar a conversa** —
registrado no log como `eco_sem_id`; aceite §12 do CHECKLIST mede se acontece.

## 7. Memória

Janela por conversa: últimas `limites.memoria_mensagens` (padrão 20) dentro de
`limites.memoria_horas` (padrão 24). Grava texto do cliente e texto final do agens; chamadas de
ferramenta não entram na memória (o contexto do turno traz o estado vivo). Áudio entra como
`[áudio 12s] <transcrição>`; anexo recusado como `[imagem]`, `[documento]` etc.

## 8. Log por turno (auditoria e custo)

Uma linha por turno, sem texto de mensagem nem argumento de ferramenta:

| Campo | Conteúdo |
|---|---|
| `modo` | `llm` · `fastpath` · `fixa` (resposta fixa sem LLM) · `pausado` · `erro` |
| `mensagem_ids` | ids de canal que compuseram o turno (rajada) |
| `modelo`, `iteracoes` | |
| `ferramentas` | lista `{nome, status_http, codigo_erro}` |
| `tokens_entrada`, `tokens_saida` | somados do turno (inclui transcrição pelo Gemini) |
| `custo_micro_usd` | estimado pela tabela de preço da config (05 §7) |
| `latencia_ms` | da saída da fila até o envio da resposta |
| `resultado` | `ok` · `erro_llm` · `erro_sistema` · `erro_envio` · `ticket_invalido` |
| `escalou` | 0/1 |

Agregação por agente/tenant/dia é consulta SQL; não há tabela de agregado na F1.

## 9. Retenção e apagamento

- **90 dias** (`AGENS_RETENCAO_DIAS`) de texto completo. Job diário: `mensagens.conteudo = NULL`
  para o que passou; a linha fica (metadado). Eventos e ecos com mais de 7 dias são apagados.
  Turnos ficam (não têm texto).
- **Apagamento sob pedido do titular:** `DELETE /admin/agentes/{a}/tenants/{t}/contatos/{c}`
  (token `AGENS_ADMIN_TOKEN`) apaga mensagens e conversa e troca `contato` por
  `sha256(contato)` nos turnos. Idempotente.

## 10. Schema (SQL portável — B1)

Regras para o mesmo SQL valer em SQLite e Postgres: ids `TEXT` (uuid v7 gerado em Go); instantes
`INTEGER` (ms UTC, gerados em Go, nunca `DEFAULT now()`); booleanos `INTEGER` com `CHECK (x IN
(0,1))`; JSON em `TEXT`; sem `AUTOINCREMENT`, sem `strftime`, sem `ON CONFLICT ... DO UPDATE` com
sintaxe exclusiva (upsert feito como `INSERT` + `UPDATE` em transação, ou `INSERT ... ON CONFLICT
(cols) DO NOTHING`, que os dois aceitam). PRAGMAs ficam no código de abertura do SQLite, não na
migration.

```sql
-- 0001_init.sql
CREATE TABLE schema_version (versao INTEGER NOT NULL);

CREATE TABLE conversas (
  agente        TEXT NOT NULL,
  tenant        TEXT NOT NULL,
  contato       TEXT NOT NULL,
  pausada_ate   INTEGER,              -- ms UTC; NULL = ativa
  config_json   TEXT,                 -- última config combinada recebida do sistema
  config_em     INTEGER,
  criada_em     INTEGER NOT NULL,
  atualizada_em INTEGER NOT NULL,
  PRIMARY KEY (agente, tenant, contato)
);

CREATE TABLE mensagens (
  id         TEXT PRIMARY KEY,
  agente     TEXT NOT NULL,
  tenant     TEXT NOT NULL,
  contato    TEXT NOT NULL,
  papel      TEXT NOT NULL CHECK (papel IN ('cliente','agente','humano')),
  conteudo   TEXT,                    -- NULL depois da retenção
  anexos     TEXT,                    -- JSON de []Anexo sem Ref
  criada_em  INTEGER NOT NULL
);
CREATE INDEX ix_mensagens_conversa ON mensagens (agente, tenant, contato, criada_em);
CREATE INDEX ix_mensagens_criada ON mensagens (criada_em);

CREATE TABLE eventos (
  agente      TEXT NOT NULL,
  mensagem_id TEXT NOT NULL,
  recebido_em INTEGER NOT NULL,
  PRIMARY KEY (agente, mensagem_id)
);

CREATE TABLE ecos (
  agente      TEXT NOT NULL,
  mensagem_id TEXT NOT NULL,
  criado_em   INTEGER NOT NULL,
  PRIMARY KEY (agente, mensagem_id)
);

CREATE TABLE turnos (
  id              TEXT PRIMARY KEY,
  agente          TEXT NOT NULL,
  tenant          TEXT NOT NULL,
  contato         TEXT NOT NULL,
  modo            TEXT NOT NULL CHECK (modo IN ('llm','fastpath','fixa','pausado','erro')),
  mensagem_ids    TEXT NOT NULL,      -- JSON []string
  modelo          TEXT,
  iteracoes       INTEGER NOT NULL DEFAULT 0,
  ferramentas     TEXT NOT NULL,      -- JSON []{nome,status,codigo}
  tokens_entrada  INTEGER NOT NULL DEFAULT 0,
  tokens_saida    INTEGER NOT NULL DEFAULT 0,
  custo_micro_usd INTEGER NOT NULL DEFAULT 0,
  latencia_ms     INTEGER NOT NULL DEFAULT 0,
  resultado       TEXT NOT NULL,
  escalou         INTEGER NOT NULL DEFAULT 0 CHECK (escalou IN (0,1)),
  criado_em       INTEGER NOT NULL
);
CREATE INDEX ix_turnos_agente_tenant ON turnos (agente, tenant, criado_em);

CREATE TABLE jobs (
  nome     TEXT NOT NULL,
  data_ref TEXT NOT NULL,             -- AAAA-MM-DD em AGENS_TZ
  feito_em INTEGER NOT NULL,
  PRIMARY KEY (nome, data_ref)
);
```

## 11. Interfaces (B1)

```go
type Store interface {
    // eventos e ecos
    MarcarEvento(ctx, agente, mensagemID string) (novo bool, err error)
    DesmarcarEvento(ctx, agente, mensagemID string) error
    RegistrarEco(ctx, agente, mensagemID string) error
    EhEco(ctx, agente, mensagemID string) (bool, error)
    // conversa
    Conversa(ctx, ConversaID) (Conversa, error)              // cria vazia se não existe
    Pausar(ctx, ConversaID, ate time.Time) error
    GuardarConfig(ctx, ConversaID, configJSON []byte) error
    // memória
    AnexarMensagem(ctx, ConversaID, MensagemMemoria) error
    Janela(ctx, ConversaID, max int, desde time.Time) ([]MensagemMemoria, error)
    // log
    RegistrarTurno(ctx, Turno) error
    // manutenção (§9)
    ExpurgarTexto(ctx, antesDe time.Time) (int64, error)
    ApagarEventosEcos(ctx, antesDe time.Time) (int64, error)
    ApagarContato(ctx, ConversaID) error
    JobFeito(ctx, nome, dataRef string) (bool, error)
    MarcarJob(ctx, nome, dataRef string) error
    Close() error
}

type Trava interface {
    // Garante um turno por conversa por vez, mesmo com mais de um processo (Postgres: advisory lock).
    Adquirir(ctx context.Context, chave string) (liberar func(), err error)
}
```

Implementações na F1: `store/sqlite` e `trava/memoria`. A bateria `store/storetest.Rodar(t,
func() Store)` cobre o contrato; `store/postgres` (futuro) roda a mesma bateria.
