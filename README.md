# Sola Agens — atendente da Sola

**Status:** em construção (09/10/2026). Todas as
decisões da F1 fechadas em 09/10 ([CONTRATO §10](CONTRATO.md#10-decisões-da-f1)). Fases 1–4 do build
feitas (o agens já atende de ponta a ponta com fakes). Próximo passo: fase 5 (áudio). PR do Eliza codado (contrato 07, aceite pendente).

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
