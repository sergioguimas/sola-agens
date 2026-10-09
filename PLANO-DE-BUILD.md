# Sola Agens — plano de build

Como transformar [CONTRATO.md](CONTRATO.md) + [specs/](specs/) em `bin/sola-agens`. Padrão de
modelos (o mesmo do Smaug): **Opus planeja, orquestra e revisa; Sonnet escreve o grosso do código**,
uma fase por vez, via subagentes. **Só começa depois que o CONTRATO §10 fechar.**

---

## 1. Regras do build

1. **Branch `development`**, um commit por fase, mensagem `agens: fase N — <o quê>`. Merge na `main`
   só depois da revisão Opus.
2. **A sessão principal roda em Opus.** Cada fase é delegada a um subagente **Sonnet** com o prompt
   da fase (§4). A sessão Opus lê o diff, roda `go vet ./... && go test ./...`, aplica o checklist
   (§5) e só então commita. Correção pequena: a sessão faz; estrutural: volta ao Sonnet com o parecer.
3. **Uma fase por vez, na ordem.** Não avançar com teste vermelho ou `vet` sujo.
4. **Especificação manda.** Decisão que os docs não cobrem: a sessão Opus decide, anota no
   `README.md` em "Decisões durante o build" e segue. Se contraria o contrato, para e pergunta.
5. **Sem rede nos testes.** Gemini e sistemas só por interface + fake. A spec real do Eliza vai para
   `testdata/` como golden. O aceite (CONTRATO §8) é manual, contra o Eliza de desenvolvimento.
6. **Não tocar no Smaug.** Pacotes genéricos são copiados (A3), não importados.

## 2. Prompt de abertura (colar na sessão Opus)

```
Você está em Projetos/sola-agens. Vamos construir o serviço seguindo, nesta ordem, CONTRATO.md,
specs/01..05 e PLANO-DE-BUILD.md. Leia todos antes de qualquer coisa, e também
redmilab/servicos/smaug/specs/05-arquitetura-go.md e os pacotes do Smaug listados em
specs/05 §2 (padrões da casa).

Regras: uma fase por vez conforme PLANO-DE-BUILD §3; cada fase é delegada a um subagente Sonnet com o
prompt de §4 mais o contexto necessário; você (Opus) revisa com o checklist de §5, roda go vet e go
test, corrige o que for pequeno, devolve ao Sonnet o que for estrutural, e commita
"agens: fase N — ...". Decisões fora do spec vão em README.md → "Decisões durante o build".

Comece pela fase 1.
```

## 3. Fases da F1 (agente Eliza)

| # | Entrega | Pacotes | Prova |
|---|---|---|---|
| 1 | Esqueleto que sobe | `go.mod`, `main.go`, `config`, `httpapi` (`/health`, `/v1/mensagens` só com assinatura e `202`), log, encerramento limpo | `go run .` sobe; testes de config e de assinatura |
| 2 | Base e agentes | `store` (migrações, memória, eventos, pausa, log por turno), `agentecfg` | testes de store e de validação de agente |
| 3 | OpenAPI e conector | `openapi` (spec → `ToolDecl`), `conector` (HTTP + identidade + envelope), `llm` copiado | golden da spec do Eliza → ferramentas; conector com `httptest` |
| 4 | Agente | `agent` (loop, prompt base + persona, fastpath), `worker`, debounce; ligação `httpapi → worker → agent → conector` | ponta a ponta com fakes: mensagem encaminhada → `POST /mensagens` do sistema |
| 5 | Áudio | roteador e transcrição copiados do Smaug | testes do roteador |
| 6 | Fechamento | `README.md` (operação + decisões do build), Dockerfile, bateria de avaliação, aceite contra o Eliza de dev | CHECKLIST-ACEITE preenchido |

## 4. Prompts por fase (para o subagente Sonnet)

**A preencher** quando o contrato fechar, no formato do Smaug (contexto, arquivos a ler, entrega,
critérios de pronto).

## 5. Checklist de revisão (sessão Opus, por fase)

Herdado do Smaug, mais:
- [ ] Nenhum identificador de cliente (org, telefone) sai de argumento do LLM.
- [ ] Ferramenta só existe se a operação tem `x-atendente: true`.
- [ ] Nenhuma escrita é retentada automaticamente.
- [ ] Texto de mensagem e argumentos fora do log contínuo.
- [ ] `go vet` e `go test` limpos; sem rede nos testes.

## 6. Depois do build

Deploy em container na VPS da Sola; `ATENDENTE_URL` e `ATENDENTE_ENCAMINHAMENTO_SECRET` no Eliza;
agente ligado primeiro só para a org de teste (`admin`).
