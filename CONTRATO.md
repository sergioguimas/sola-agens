# Sola Agens — contrato do serviço

**Status:** modelo montado em 09/10/2026 a partir do Smaug (`redmilab/servicos/smaug`). Decisões de
origem fechadas ([docs/DECISOES.md](docs/DECISOES.md)); todas as decisões da F1 fechadas
em 09/10 (§10). Próximo passo: preencher specs 01–05, plano de build e aceite.
**Papel:** plataforma interna da Sola para pôr um atendente (LLM com ferramentas) em qualquer
sistema da Sola. O motor lê a **OpenAPI** do sistema, transforma as operações liberadas em
ferramentas e conversa com o cliente final pelo canal (WhatsApp; widget web na F2).
**Origem do código:** motor do Smaug (LLM, loop de ferramentas, fila por chat, fastpath, áudio,
imagem). O que é financeiro fica no Smaug.

Documentos que detalham este contrato:

| Doc | Conteúdo |
|---|---|
| [specs/01-dominio-e-schema.md](specs/01-dominio-e-schema.md) | agente, conversa, memória, dedupe, log por turno, retenção, schema |
| [specs/02-ferramentas-e-prompt.md](specs/02-ferramentas-e-prompt.md) | conector OpenAPI → ferramentas, prompt base + persona do agente, fastpath |
| [specs/03-fluxos.md](specs/03-fluxos.md) | passo a passo de cada fluxo, incluindo falhas |
| [specs/04-integracoes.md](specs/04-integracoes.md) | entrada encaminhada (Eliza), gateway WhatsApp, Gemini, chamadas aos sistemas |
| [specs/05-arquitetura-go.md](specs/05-arquitetura-go.md) | pacotes, o que vem do Smaug, concorrência, testes |
| [PLANO-DE-BUILD.md](PLANO-DE-BUILD.md) | fases, prompts por fase, revisão |

---

## 1. Fronteiras

```
 Eliza ──POST /v1/mensagens (assinado)──▶ sola-agens  (agente "eliza", modo encaminhado)
                                            │ GET  {sistema}/openapi.json        (ferramentas)
                                            │ POST Gemini generateContent       (function calling)
                                            │ base própria                       (memória, dedupe, log, config)
                                            ├─ {sistema} operações x-atendente   (com o ticket do turno)
                                            └─ {sistema} POST /mensagens         (resposta ao cliente, modo encaminhado)
```

- **Entrada (F1):** mensagem encaminhada pelo sistema, assinada (contrato do Eliza:
  `Eliza/docs/contratos/autoatendimento/06-mensagens-e-encaminhamento.md`). Canal próprio
  (WhatsApp direto, widget web) entra nas fases seguintes.
- **Saída:** no modo encaminhado, a resposta volta **pelo sistema** (o agens não tem a chave do
  WhatsApp do tenant).
- **Não faz:** não acessa banco de nenhum sistema; não executa operação que a OpenAPI não marque como
  liberada; não decide identidade do cliente (vem do ticket/asserção); não guarda mídia.
- **Onde roda o pesado:** a LLM e a transcrição são externas. No servidor: HTTP, fila e a base
  própria. RAM alvo < 50 MB.

## 2. Gatilho — o que o agens processa

| Regra | Valor | Por quê |
|---|---|---|
| Origem | `POST /v1/mensagens` com `X-Eliza-Assinatura` válida e `X-Eliza-Timestamp` recente (§5) | só o sistema dono do agente encaminha |
| Agente | resolvido pela origem assinada (segredo por agente), nunca pelo corpo | um sistema não fala em nome de outro |
| `deMim` | não vai ao agente; **pausa** a conversa (intervenção humana) | o dono respondeu pelo celular |
| Tipo | texto → agente; áudio → roteador (§3, herdado do Smaug); imagem, documento, vídeo → resposta fixa na v1, mapeados em `anexos` (A8); `nao_suportado` → resposta fixa | |
| Idempotência | `mensagemId` já processado → `202` sem reprocessar | o sistema não retenta, mas a Evolution sim |
| Rajada | mensagens do mesmo contato em ~3 s viram um turno só | "oi" / "queria marcar" / "amanhã" |
| Ordem | uma conversa por vez, na ordem de chegada (fila por chat do Smaug) | |

## 3. Comportamento (v1 = F1, agente Eliza)

O agente faz o que a OpenAPI do sistema libera. Para o Eliza, na F1: consultar contexto, serviços,
profissionais e horários; listar, criar, remarcar, cancelar e confirmar os próprios agendamentos; ler
e manter o próprio cadastro; chamar um humano. Regras de negócio são do sistema — o agens só repassa
a mensagem de erro do sistema ao cliente, em linguagem natural.

Identificação: telefone → nome → cadastro (§10, M2); o nome nunca dá acesso (B3). `ambiguo` escala (contrato 02 do Eliza).
**A preencher na F1:** lista final de ferramentas e fastpath a partir do dicionário (M3).

## 4. Resposta — como o texto volta

Herdado do Smaug: texto curto para WhatsApp, `*negrito*` como único markdown, mensagem longa dividida.
Falha do LLM ou do sistema → resposta fixa ("não consegui agora, um atendente vai te responder") e
escalonamento. **A preencher:** textos fixos.

## 5. Segurança e privacidade

- **Assinatura de entrada:** `hex(HMAC-SHA256(segredo_do_agente, timestamp + "." + corpo))`,
  `timingSafeEqual`, timestamp com no máximo 5 min de diferença.
- **Identidade do cliente:** nunca vem do LLM. No modo encaminhado, o ticket que chega com a mensagem
  é repassado ao sistema em toda chamada de ferramenta daquele turno; o sistema valida.
- **Credenciais:** só o token de serviço de cada sistema e a chave do Gemini, por env. Nenhuma
  credencial de banco ou de WhatsApp de sistema.
- **LLM:** Gemini em **plano pago** (o gratuito pode usar o conteúdo).
- **Retenção:** 90 dias de texto completo, depois só metadados; apagamento sob pedido do titular.
- **Log:** sem texto de mensagem nem argumento de ferramenta no log contínuo (`LOG_DEBUG` pontual,
  como no Smaug).

## 6. Configuração

Ver `.env.example`. Configuração **de agente** (persona, instruções, sistema, OpenAPI, segredos por
referência de env, modelo): **em camadas** — padrão do motor → sistema → tenant (§10, A4; onde cada camada mora: B2).

## 7. Operação

Container na VPS da Sola. **A decidir:** rede interna apenas (F1) ou rota pública pelo Traefik (A7).
`/health`, log JSON (`slog`), métricas de custo por agente no log por turno.

## 8. Critérios de aceite (F1)

Preenchidos em [CHECKLIST-ACEITE.md](CHECKLIST-ACEITE.md) quando o contrato fechar. Base: bateria fixa
de conversas do agente Eliza (marcar, remarcar, cancelar, confirmar, desconhecido, ambíguo, injeção de
prompt, fora de escopo, sistema fora do ar, rajada, intervenção humana).

## 9. Fora do escopo — backlog

Canal web e modo anônimo (F2, site); asserção do motor e envio de arquivo (F3, Axios); confirmação
antes de gravar (F4, SolaBridge); painel de configuração; API oficial do WhatsApp.

## 10. Decisões da F1

### Fechadas — rodada 2 (09/10/2026, com o Sérgio)

| # | Decisão | Consequência |
|---|---|---|
| A1 | **Multi-tenant desde o dia 1.** Toda linha da base é chaveada por `agente + tenant` (tenant = a conta do cliente dentro do sistema, ex.: a org do Eliza). Base **própria do agens**, nunca no banco de um sistema. Acesso à base e trava por conversa **atrás de interface** (`Store`, `Trava`) | Mais de um cliente do Eliza e mais de um sistema funcionam no mesmo processo sem mudar o schema. SQLite agora; Postgres entra como segunda implementação da interface quando houver mais de um processo (B1). Revê a E4 |
| A2 | **Gemini 2.5 Flash**, temperatura 0, raciocínio 0 | ~US$ 0,015–0,03 por conversa. Revê a E3 |
| A3 | **Smaug é o molde, não uma dependência.** Os pacotes são copiados e o agens evolui separado; não há plano de módulo comum | Correção no Smaug só chega aqui se alguém trouxer na mão |
| A4 | **Configuração em camadas: padrão do motor → sistema → tenant.** Cada sistema é dono da sua configuração; no agens só ficam os padrões de fallback e o registro de cada sistema (como achá-lo e autenticar) | Onde exatamente cada camada mora: B2 |
| A5 | **OpenAPI lida em tempo de execução**: baixada no boot, cache, recarga por sinal | Spec inválida derruba o boot. Revê a E1 |
| A6 | **`x-atendente: true`** nas operações liberadas da OpenAPI do Eliza | Um PR no Eliza |
| A7 | **Só rede Docker interna** na F1; rota pública na F2 | Teste de fora exige túnel |
| A8 | **Texto e áudio na v1.** O modelo de mensagem já tem `anexos` (tipo, mime, tamanho, referência no canal) e cada agente tem política de mídia; sem armazenamento de arquivo | Imagem, documento e vídeo recebem resposta fixa; ligar depois é configuração e não mudança de schema |
| M1 | **Pausa por intervenção humana: 12 h por padrão, configurável por sistema.** Cada nova mensagem humana (`deMim`) reinicia o prazo | |
| M2 | **Identificação no Eliza:** telefone (já vem no ticket) → se não achar, pergunta o nome → se ainda não achar, fluxo de cadastro | A busca por nome tem risco de segurança: B3 |
| M3 | **Dicionário de respostas padrão** alimenta o fastpath e as respostas fixas. Ponto de partida: as listas de confirmação/cancelamento e as mensagens do webhook do Eliza (`web/app/api/webhooks/whatsapp/.../route.ts`, `web/lib/domain/mensagens.ts`) | Pode ser sobrescrito por sistema e por tenant (A4) |
| M4 | **Textos e limites do motor são só fallback.** Persona, tom, vocabulário e respostas são personalizáveis por sistema e por tenant (no Eliza já existe o vocabulário por nicho em `organizacao.termos` do `/contexto`) | |

### Fechadas — rodada 3 (09/10/2026, com o Sérgio)

| # | Decisão | Consequência |
|---|---|---|
| B1 | **Sem réplicas agora.** Roda **SQLite**, mas com o caminho para Postgres preparado: acesso só pelas interfaces `Store`/`Trava`; schema em SQL portável (ids `TEXT`, instantes em `INTEGER` ms UTC, sem tipo ou função exclusiva do SQLite, sem `AUTOINCREMENT`); migrations numeradas que valem para os dois; uma bateria de testes de contrato do `Store` que roda contra qualquer implementação. Postgres e advisory lock não são implementados agora | Ligar réplicas depois = escrever `store/postgres` e rodar a mesma bateria, sem mudar o domínio |
| B2 | **O sistema entrega a configuração já combinada (sistema + tenant) junto com o contexto do turno.** No Eliza: campo `atendente` novo no `GET /contexto` (persona, tom, horas de pausa, respostas padrão), lido de `autoatendimento_config`. O agens só aplica o próprio fallback por cima. A pausa (`deMim`, sem ticket) usa a última configuração vista da conversa, ou 12 h | Um PR no Eliza (coluna + campo no contexto + Zod/OpenAPI) |
| B3 | **O nome nunca dá acesso.** Se o nome bate com um cadastro de outro telefone, o agens não mostra nem age sobre esse cadastro: escala para a equipe vincular o número novo e avisa o cliente. **`nomeExibicao` (o `pushName` do WhatsApp) não é fator de segurança**: é o nome que o próprio remetente põe no perfil e pode ser trocado por qualquer um. Uso permitido só como conveniência: se bate com o cadastro do *mesmo telefone*, o agens cumprimenta pelo nome; se o telefone não achou ninguém, pode perguntar "você é a Maria?" em vez de pedir o nome do zero — e a resposta continua sem dar acesso | Endpoint novo no Eliza que responde só se existe cadastro com aquele nome (sem dados). Quem trocou de número depende da equipe |
| B4 | **Módulo de treinamento: aceito como direção, com ressalvas.** A §11 fica como rascunho; o desenho é rediscutido em detalhe quando o módulo entrar no plano (depois da F2) | Nada da F1 depende dele; a camada de tenant da A4 já é onde o perfil vai entrar |

## 11. Módulo de treinamento (rascunho — B4, rediscutir antes de construir)

**O que é:** um modo de conversa em que o interlocutor é o **operador** (o usuário do sistema, ex.: a
dona do salão no Eliza), não o cliente final. O agens conduz uma entrevista guiada e, ao fim, gera um
**perfil do tenant**.

- **Saída é um perfil estruturado, não aprendizado livre:** tom e forma de tratamento, saudações e
  despedidas, vocabulário, regras do negócio em texto ("não marco nada depois das 18h de sexta"),
  perguntas frequentes com resposta, respostas padrão. Não há fine-tuning: o perfil entra no prompt,
  na camada do tenant (A4).
- **Rascunho → aprovação:** o operador revisa o que foi gerado e aprova; só a versão aprovada vale.
  Perfil versionado, com volta para a versão anterior.
- **Limites:** o perfil nunca sobrepõe as regras de segurança do motor nem libera ferramenta; regra de
  negócio de verdade (horário, preço) continua no sistema, e o perfil só fala dela.
- **Identidade:** o sistema afirma que quem conversa é operador daquele tenant (asserção, como no
  modo da F3); um cliente final nunca entra nesse modo.
- **Onde fica:** na base do agens, por `agente + tenant`, exposto ao sistema por API para mostrar e
  editar. É exceção à A4 porque é dado que o próprio agens produz.
- **Canal:** painel do sistema (chat web). Pelo WhatsApp não dá: no modo encaminhado, a mensagem do
  dono pelo celular é tratada como intervenção humana.
- **Fase recomendada:** logo depois da F2, que entrega o canal web; piloto com um tenant do Eliza.
- **Depois (fora desta proposta):** sugerir ajustes ao perfil a partir das respostas reais do humano
  (`deMim`). Isso envolve dado de cliente final no treinamento, então exige decisão de LGPD própria.
