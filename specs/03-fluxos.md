# 03 — Fluxos

> **Esqueleto** no formato do Smaug (F-numerados, cada um com passos e falhas). Preencher no
> fechamento do contrato da F1.

---

## F1 — Mensagem encaminhada (o fluxo principal)

Sistema assina e envia → agens valida assinatura → dedupe → `deMim`? pausa e sai → conversa
pausada? ignora → debounce da rajada → fila da conversa → contexto do sistema → LLM + ferramentas →
resposta pelo sistema → log do turno. **A preencher:** passos e falhas.

## F2 — Ferramenta que falha

Erro de regra do sistema (409/422) volta ao LLM como dado; erro técnico (5xx/timeout) aborta o turno,
manda resposta fixa e escala. **A preencher.**

## F3 — Intervenção humana (pausa)

## F4 — Escalonamento

## F5 — Áudio (herdado do Smaug F11)

## F6 — Subida, recarga de OpenAPI e encerramento

## F7 — Mensagens ignoradas (sem resposta por definição)
