# 03 — Fluxos

Cada fluxo do começo (mensagem chegando) ao fim (resposta saindo), com os pontos de falha. Regras em
[01](01-dominio-e-schema.md), ferramentas e prompt em [02](02-ferramentas-e-prompt.md), contratos
externos em [04](04-integracoes.md).

---

## F1 — Mensagem encaminhada (o fluxo principal)

```
Eliza ──POST /a/{agente}/v1/mensagens──▶ httpapi
  1. agente não registrado → 404
  2. corpo > 64 KB → 413
  3. X-Eliza-Timestamp ausente ou |agora − ts| > 300 s → 401 (log "timestamp")
  4. HMAC-SHA256(segredo do agente, ts + "." + corpo bruto) ≠ X-Eliza-Assinatura
     (comparação em tempo constante) → 401 (log "assinatura")
  5. JSON inválido ou versao ≠ 1 → 400
  6. MarcarEvento(agente, mensagemId) já existia → 202 (log "duplicado")   ← 202, senão o Eliza cai no fallback
  7. deMim → F3; responde 202
  8. ticket ausente com deMim=false → 400 (contrato quebrado do lado do sistema)
  9. normaliza para Mensagem (01 §5); entrega ao debounce (F8); responde 202 { aceito: true }
     fila da conversa cheia → DesmarcarEvento, 503 (o Eliza cai no fluxo de palavra-chave — aceito)
worker (um por conversa, sequencial; Trava pela chave da conversa)
 10. conversa pausada (pausada_ate > agora) → grava mensagem na memória, turno modo=pausado, fim
 11. só anexos e todos com política resposta_fixa → responder(respostas.midiaNaoSuportada),
     turno modo=fixa, fim
 12. anexo de áudio com política processar → F5 (vira texto) e segue
 13. contexto: GET obterContexto com o ticket mais recente → GuardarConfig(atendente combinado)
 14. fastpath (02 §6) → se aplicou, pula para 17 com a resposta pronta (modo=fastpath)
 15. monta prompt (02 §7) + memória (01 §7) + mensagens do turno
 16. loop do LLM (04 §3): Gemini → functionCall? → conector → functionResponse → … até texto final
     ou limites.max_iter
 17. responder: divide em blocos ≤ 4000 (quebra em parágrafo, depois frase) e envia em ordem;
     RegistrarEco de cada mensagemId devolvido
 18. memória: cliente (texto do turno) + agente (resposta); RegistrarTurno
```

Falhas:

| Ponto | Falha | O que acontece |
|---|---|---|
| 13 | `TICKET_*`, `ADDON_INACTIVE`, `UNAUTHORIZED`, `VERSION_MISMATCH` | turno `resultado=ticket_invalido`, **sem resposta** (o próprio envio exigiria ticket válido); log de erro |
| 13 | 5xx, timeout, rede | F2 (resposta fixa + escalar), que provavelmente também falha; log |
| 16 | Gemini 429/5xx/timeout após retries | F2 |
| 16 | resposta vazia (aconteceu no Smaug com Flash-Lite) | 1 nova chamada sem tools pedindo só o texto final; vazia de novo → houve escrita com sucesso no turno? "Feito! Mais alguma coisa?" : `respostas.falhaTecnica` + escalar |
| 16 | `max_iter` atingido | `respostas.falhaTecnica` + escalar |
| 17 | `WHATSAPP_UNAVAILABLE` ou erro de envio | `resultado=erro_envio`, log; **não reenvia** (a escrita, se houve, já está no sistema) |
| — | timeout do turno (`limites.turno_timeout_s`, 90 s) | contexto cancelado; F2 |

## F2 — Turno que aborta por erro técnico

Disparo: código da lista 02 §2.1 em ferramenta, falha do LLM depois dos retries, `max_iter`,
timeout do turno.

1. Cancela o loop (nenhuma ferramenta nova é chamada).
2. `responder(respostas.falhaTecnica)` — se o próprio envio falhar, só log.
3. `escalar(motivo=falha_tecnica|outro, resumo="Falha técnica no atendimento automático: <etapa>")`
   — sem texto do cliente no resumo.
4. Memória grava só a mensagem do cliente (o próximo turno tem o contexto).
5. Turno com `resultado=erro_llm|erro_sistema`, `escalou=1`.

Erro **de regra** (4xx fora da lista) não é F2: volta ao LLM como dado (02 §2).

## F3 — Intervenção humana (`deMim`)

1. `EhEco(agente, mensagemId)` → ignora (log `eco`), fim.
1a. `aviso` no corpo (envio automático do sistema: lembrete, notificação — A11, contrato 09 do Eliza) →
   cria a conversa se não existe, grava o texto com `papel=aviso` (o modelo o vê como
   `[aviso automático enviado pelo estabelecimento] ...`), log `aviso tipo=<tipo>`; **sem pausa e sem
   turno**, fim.
2. Senão: `pausaHumanaHoras` de `conversas.config_json` ou padrão (12 h) → `Pausar(agora + h)`.
3. Se o corpo trouxe texto, grava na memória com `papel=humano` (o agens, ao voltar, sabe o que a
   equipe disse).
4. Log `pausa ate=<instante>`; sem turno (não houve resposta).

**Corrida do eco:** o eco pode chegar ao agens enquanto o turno que o enviou ainda roda. Por isso o
`deMim` entra na **mesma fila da conversa** (nunca em paralelo) e só é avaliado depois que o turno
anterior terminou — e o turno registra cada eco logo após o `POST /mensagens` responder.

## F4 — Escalonamento

Pelo LLM (`chamar_humano`) ou pelo motor (F2). Executa `escalar` com o ticket do turno. Resposta
`equipeNotificada=false` → o LLM usa `respostas.equipeSemContato`. Limite do Eliza (`aa-escalar`, 3/h)
estourado → `RATE_LIMITED` → aborta o chamar_humano, que volta ao LLM como `{ equipeNotificada:
false }` (exceção à 02 §2.1: escalar não pode derrubar o turno).

Escalar **não** pausa a conversa: quem pausa é a resposta humana (F3).

## F5 — Áudio

1. Política do agente para `audio` = `resposta_fixa` → F1 passo 11.
2. `DuracaoS > limites.audio_max_s` → `respostas.audioLongo`, modo=fixa, fim (sem baixar).
3. `GET baixarMidia(mensagemId)` com o ticket **daquela** mensagem (o ticket é por mensagem; na
   rajada, cada áudio usa o seu). Resposta `{ mime, tamanho, base64 }` (Eliza 07 §4.2).
   `NOT_FOUND` → `respostas.audioNaoChegou`, fim. `MEDIA_TOO_LARGE` →
   `respostas.audioLongo`, fim. 5xx/`WHATSAPP_UNAVAILABLE` → F2.
4. `Tamanho` informado na mensagem > 16 MB → `respostas.audioLongo` antes de baixar, fim.
5. `transcricao.Transcrever(bytes, mime)` (Gemini ou Groq — 04 §4). Falha → `respostas.audioFalhou`,
   sem escalar (o cliente pode escrever).
6. Texto vira a mensagem do turno; memória `"[áudio Ns] " + texto`. Segue no F1 passo 13.
7. Bytes descartados ao fim do passo 5. Nada de áudio no disco ou no log.

## F6 — Subida, recarga e encerramento

Subida: config (env) → registro de agentes (`agentes/*.yaml`, valida) → abre Store e roda migrations
→ baixa e converte as OpenAPIs (02 §1; falha = sai) → provider LLM → worker → manutenção (F9) → HTTP.
`/health` só responde 200 depois de tudo isso.

`SIGHUP`: recarrega `agentes/*.yaml` e as OpenAPIs; inválido mantém o anterior e loga.

`SIGTERM`/`SIGINT`: para de aceitar HTTP (`Shutdown` com 10 s), fecha o debounce (descarrega o que
estiver acumulado para a fila), espera o worker até `AGENS_ENCERRAMENTO_S` (30 s), fecha o Store.
Turno interrompido no meio fica sem resposta (o evento já foi marcado; aceito e logado).

## F7 — Mensagens que não geram resposta, por definição

| Caso | Onde é cortada |
|---|---|
| Assinatura ou timestamp inválidos | F1 passos 3–4 |
| Duplicada | F1 passo 6 |
| Eco do próprio agens | F3 passo 1 |
| Aviso do sistema (lembrete, notificação) | F3 passo 1a (vira contexto) |
| `deMim` da equipe | F3 (pausa) |
| Conversa pausada | F1 passo 10 |
| Ticket inválido no contexto | F1 passo 13 |

Grupo, status e org demo nem chegam (o Eliza não encaminha).

## F8 — Rajada (debounce)

Antes da fila, por conversa:

- Primeira mensagem abre uma janela de `limites.rajada_ms` (3000 ms). Cada mensagem nova reinicia a
  espera, até o teto de `limites.rajada_max_ms` (8000 ms) desde a primeira.
- Ao fechar: um único item na fila com todas as mensagens em ordem de `recebidaEm`, o **ticket mais
  recente** e os anexos de cada uma.
- No LLM, as mensagens da rajada entram como um único turno do cliente, separadas por quebra de
  linha.
- `deMim` não passa pelo debounce: entra direto na fila da conversa (F3), mas **depois** de
  descarregar a rajada aberta daquela conversa, para manter a ordem.

## F9 — Manutenção (diária)

Job diário às `AGENS_MANUTENCAO_HORA` (04:00) com catch-up de 3 dias, mesmo padrão do scheduler do
Smaug (`(nome, data_ref)` só marcado após sucesso): `ExpurgarTexto(agora − 90 d)` e
`ApagarEventosEcos(agora − 7 d)`. Log com as contagens.

## F10 — Apagamento sob pedido do titular

`DELETE /admin/agentes/{a}/tenants/{t}/contatos/{c}` com `Authorization: Bearer AGENS_ADMIN_TOKEN`
→ `ApagarContato` (01 §9) → 204 (também quando não havia nada). Token errado → 401. A rota só existe
se `AGENS_ADMIN_TOKEN` estiver definido.
