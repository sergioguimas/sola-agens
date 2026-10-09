# 04 — Integrações

> **Esqueleto.** O lado do Eliza já está contratado e implementado; aqui fica o lado do agens.

---

## 1. Entrada encaminhada (Eliza)

Contrato de origem: `Eliza/docs/contratos/autoatendimento/06-mensagens-e-encaminhamento.md` e
`Eliza/docs/API_AUTOATENDIMENTO.md`.

- `POST /v1/mensagens` com o corpo `MensagemEncaminhada` (Zod em
  `Eliza/web/contracts/autoatendimento/mensagens.ts`), headers `X-Eliza-Assinatura` e
  `X-Eliza-Timestamp`.
- Responder **202** em até 3 s (o Eliza cai no fluxo de palavra-chave se não vier 202). O
  processamento é assíncrono, na fila da conversa.
- O `ticket` da mensagem vale 30 min e é o que autoriza as chamadas de ferramenta daquele turno.

## 2. Chamadas ao sistema (ferramentas)

Eliza: headers `Authorization: Bearer <AUTOATENDIMENTO_API_TOKEN>`, `X-Autoatendimento-Ticket`,
`X-Autoatendimento-Versao: 1`. Envelope `{ data, meta? }` / `{ error: { code, message, details,
request_id } }`. Resposta ao cliente: `POST /api/v1/autoatendimento/mensagens`.
**A preencher:** timeouts e retentativa (nunca em escrita).

## 3. Gemini API

Herdado do Smaug (`specs/04 §2`): REST sem SDK, function calling, retry em 429/5xx, interface
`Provider`. Plano pago obrigatório.

## 4. Transcrição

Herdado do Smaug (`specs/04 §2.6`).
