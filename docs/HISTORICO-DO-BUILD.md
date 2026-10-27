# Histórico do build

Origem do código e decisões tomadas durante o build da F1 (fases 1–6, 09/10/2026), registradas fase a
fase conforme o [PLANO-DE-BUILD](../PLANO-DE-BUILD.md). As decisões de produto e arquitetura ficam no
[CONTRATO §10](../CONTRATO.md#10-decisões-da-f1) e em [DECISOES.md](DECISOES.md).

## Origem do código

`internal/llm` e `internal/transcricao` foram copiados do Smaug (`redmilab/servicos/smaug`; `internal/llm`
no commit `b81d795`) e evoluem separados; o restante é código novo no molde do Smaug ([specs/05 §2](../specs/05-arquitetura-go.md#2-mapa-de-reaproveitamento-do-smaug)).

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
- Dockerfile: `/data` é criado no estágio de build e copiado com `--chown=65532:65532` (a imagem final não tem shell). `env_file` sobrescreve o `ENV` da imagem: o `.env` de produção não deve repetir `AGENS_DATA_DIR`/`AGENS_AGENTES_DIR` do exemplo (documentado em [OPERACAO](OPERACAO.md#container)).
- `go test -race ./...` rodado no container `golang:1.26`, limpo (o Windows de desenvolvimento não tem cgo).
- Avaliação (`avaliacao/`): usa a `atendimento.Fabrica` e o `agent.Motor` reais, com o Eliza simulado em `httptest` servindo a spec real (golden do contrato 07) e o Gemini real; sem HTTP de entrada nem debounce (cada rodada é um `agent.Turno`). Um ticket por cenário liga ao estado do simulado; cada repetição usa uma conversa nova num SQLite temporário. O relógio é fixo no mundo (qua 14/10/2026 10:00) e avança 1 s por leitura, para a memória manter ordem e janela. A latência reportada é a real; a do turno gravado vem desse relógio.
- O simulado é estrito como o Eliza: corpo com campo desconhecido → 422, horário fora da grade ou ocupado → `SLOT_UNAVAILABLE` com sugestões, antecedência, janela e limite de ativos validados. Não replica a fila de aprovação nem a UI.
- Cenários 16–18, 19–21, 22 e 24 ficam fora da avaliação (infraestrutura, transcrição real ou sem dependência do modelo); seguem no aceite manual.
- **Revisão Opus:** `gofmt` aplicado em `internal/agent/run.go`; `-race` rodado no container `golang:1.26` sobre as fases 1–5 (limpo); imagem construída e executada com um Eliza falso no host — boot completo (12 ferramentas), base gravada como não-root no volume, `HEALTHCHECK` → healthy, 23 MB. Cenário 25 na avaliação: pushName malicioso não vira instrução (cobre a correção da fase 4).
