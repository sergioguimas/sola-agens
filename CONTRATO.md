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
| Origem | `POST /a/{agente}/v1/mensagens` (specs/04 §1) com `X-Eliza-Assinatura` válida e `X-Eliza-Timestamp` recente (§5) | só o sistema dono do agente encaminha |
| Agente | resolvido pela origem assinada (segredo por agente), nunca pelo corpo | um sistema não fala em nome de outro |
| `deMim` | não vai ao agente; eco da própria resposta é ignorado; o resto **pausa** a conversa por 12 h (configurável) | o dono respondeu pelo celular |
| Tipo | texto → agente; áudio → transcrito e tratado como texto (specs/02 §8); imagem, documento, vídeo → resposta fixa na v1, mapeados em `anexos` (A8); `nao_suportado` → resposta fixa | |
| Idempotência | `mensagemId` já processado → `202` sem reprocessar (qualquer outro código faria o Eliza cair no fallback) | o sistema não retenta, mas a Evolution sim |
| Rajada | mensagens do mesmo contato em ~3 s (teto 8 s) viram um turno só | "oi" / "queria marcar" / "amanhã" |
| Ordem | uma conversa por vez, na ordem de chegada (fila por chat do Smaug) | |

## 3. Comportamento (v1 = F1, agente Eliza)

O agente faz o que a OpenAPI do sistema libera. Para o Eliza, na F1: consultar contexto, serviços,
profissionais e horários; listar, criar, remarcar, cancelar e confirmar os próprios agendamentos; ler
e manter o próprio cadastro; chamar um humano. Regras de negócio são do sistema — o agens só repassa
a mensagem de erro do sistema ao cliente, em linguagem natural.

Identificação: telefone → nome → cadastro (§10, M2); o nome nunca dá acesso (B3). `ambiguo` escala (contrato 02 do Eliza).
Ferramentas: [specs/02 §3](specs/02-ferramentas-e-prompt.md#3-ferramentas-da-f1-agente-eliza). Fastpath: "sim"/"cancelar" exatos com **um** agendamento aguardando confirmação ([specs/02 §6](specs/02-ferramentas-e-prompt.md#6-fastpath)).

## 4. Resposta — como o texto volta

Herdado do Smaug: texto curto para WhatsApp, `*negrito*` como único markdown, mensagem longa dividida.
Falha do LLM ou do sistema → resposta fixa ("não consegui agora, um atendente vai te responder") e
escalonamento. Textos fixos: dicionário do motor ([specs/02 §5](specs/02-ferramentas-e-prompt.md#5-dicionário-de-respostas-padrão-m3)),
sobrescrito por sistema e tenant.

## 5. Segurança e privacidade

- **Assinatura de entrada:** `hex(HMAC-SHA256(segredo_do_agente, timestamp + "." + corpo))`,
  `timingSafeEqual`, timestamp com no máximo 5 min de diferença.
- **Identidade do cliente:** nunca vem do LLM. No modo encaminhado, o ticket que chega com a mensagem
  é repassado ao sistema em toda chamada de ferramenta daquele turno; o sistema valida.
- **Credenciais:** só o token de serviço de cada sistema e a chave do Gemini, por env. Nenhuma
  credencial de banco ou de WhatsApp de sistema.
- **LLM:** Gemini em **plano pago** (o gratuito pode usar o conteúdo).
- **Retenção:** 90 dias de texto completo, depois só metadados; apagamento sob pedido do titular.
- **Log:** sem texto de mensagem, transcrição, ticket nem argumento de ferramenta no log contínuo
  (`AGENS_LOG_DEBUG` pontual, como no Smaug); contato aparece só como hash.
- **Mídia:** nenhum arquivo é guardado; áudio é baixado em memória, transcrito e descartado.

## 6. Configuração

Ver `.env.example` e [specs/05 §6–§7](specs/05-arquitetura-go.md#6-registro-do-agente). No agens: o registro
de cada sistema (`agentes/<id>.yaml`) e os padrões de fallback. Persona, tom, instruções, respostas e
horas de pausa vêm do sistema, já combinadas com o tenant, a cada turno (§10, A4/B2; specs/01 §3).

## 7. Operação

Container na VPS da Sola, só na rede Docker do Eliza na F1 (A7). `/health`, log JSON (`slog`), custo
e tokens por turno na tabela `turnos` (consulta por agente/tenant/dia). Job diário de retenção.
Apagamento por titular em `DELETE /admin/...` (specs/03 F10).

## 8. Critérios de aceite (F1)

[CHECKLIST-ACEITE.md](CHECKLIST-ACEITE.md): 24 conversas, 5 verificações de segurança e as medições
(custo, latência, RAM). Depende do PR do Eliza ([specs/04 §6](specs/04-integracoes.md#6-o-que-o-eliza-precisa-mudar)).

## 9. Fora do escopo — backlog

Canal web e modo anônimo (F2a, `/marcar` do Eliza — rodada 4) e conhecimento estático + embed (F2b, site); asserção do motor e envio de arquivo (F3, Axios); confirmação
antes de gravar (F4, SolaBridge); painel de configuração; API oficial do WhatsApp; comando para
retomar conversa pausada; imagem e documento; réplicas do processo (B1); módulo de treinamento (§11).

## 10. Decisões da F1

### Fechadas — rodada 2 (09/10/2026, com o Sérgio)

| # | Decisão | Consequência |
|---|---|---|
| A1 | **Multi-tenant desde o dia 1.** Toda linha da base é chaveada por `agente + tenant` (tenant = a conta do cliente dentro do sistema, ex.: a org do Eliza). Base **própria do agens**, nunca no banco de um sistema. Acesso à base e trava por conversa **atrás de interface** (`Store`, `Trava`) | Mais de um cliente do Eliza e mais de um sistema funcionam no mesmo processo sem mudar o schema. SQLite agora; Postgres entra como segunda implementação da interface quando houver mais de um processo (B1). Revê a E4 |
| A2 | ~~**Gemini 2.5 Flash**, temperatura 0, raciocínio 0~~ **Revista pela A9 (26/10)** | ~US$ 0,015–0,03 por conversa. Revê a E3 |
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

### Fechadas — rodada 4 (26/10/2026, com o Sérgio): canal web da `/marcar` do Eliza (F2a)

Detalhe, alternativas e motivos em [docs/RODADA-4-CANAL-WEB-MARCAR.md](docs/RODADA-4-CANAL-WEB-MARCAR.md).

| # | Decisão |
|---|---|
| W1 | Chat na página pública só **aconselha e pré-preenche** o formulário; nunca grava |
| W2 | Modo **anônimo** com **ticket de visitante** emitido pelo Eliza (`tipo:"visitante"`, `org`, `sess`, sem `tel`/`inst`, TTL 30 min), aceito só em contexto, serviços, profissionais e horários |
| W3 | Navegador → **Eliza** (`POST /api/marcar/{slug}/chat`) → agens; agens continua só na rede interna (A7 vale) |
| W4 | Turno **síncrono** `{ resposta, acoes[] }`, sem streaming; `preencher_agendamento` é **ferramenta de canal** (o motor não executa, devolve em `acoes`); o navegador valida antes de aplicar |
| W5 | Retenção do anônimo: **7 dias** de texto, depois só metadados |
| W6 | Limites: 20 msg/sessão, 60/IP/h, 500 caracteres (Eliza); teto diário de turnos por org, padrão 300 (agens); sem CAPTCHA na v1 |
| W7 | O chat **não pede dado pessoal**; aviso fixo de assistente automático |
| W8 | Liga por org: `autoatendimento_config.canal_web` (padrão `false`), exige também `ativo` |
| W9 | UI em **React dentro do Eliza**; embed genérico só na F2b |
| W10 | F2 dividida: **F2a** (`/marcar`) depois do aceite da F1; **F2b** (conhecimento estático + embed + site) em seguida |

### Fechadas — avaliação com Gemini real (26/10/2026, com o Sérgio)

| # | Decisão | Consequência |
|---|---|---|
| A9 | **Gemini 3.6 Flash**, temperatura 0, raciocínio 0. Revê a A2 (e, por ela, a E3) | Na 1ª bateria real (`go run ./avaliacao`, 17 cenários) o 2.5 Flash fez 10–11/17: respondia sem chamar ferramenta e perguntava o que já sabia. Com o prompt ajustado (0cac9a2): **3.6 Flash 34/34** (×2), 3.8 Flash 34/34 mas 6,5 s contra 3,9 s de latência média, 3.1 Flash-Lite 32/34 (falha o #8, número novo, que é de segurança). Preço pago: US$ 0,75/3,75 por milhão até 31/12/2026, **1,50/7,50 a partir de 01/01/2027** (~US$ 0,007 por turno hoje, ~0,015 depois). O 3.6 já é a geração anterior do Flash: quando for aposentado, trocar `AGENS_MODEL` e rodar a bateria de novo. Candidato seguinte: 3.8 Flash |

### Fechada — infra na VPS (26/10/2026, com o Sérgio)

| # | Decisão | Consequência |
|---|---|---|
| A10 | **O agens entra na `proxy_net`**, a rede compartilhada de todos os apps da VPS, sem labels do Traefik e sem `ports:` | Sistema novo fala com o agens sem configurar rede (o agens é de todos os sistemas, o Eliza é só o piloto). Continua não público (A7 vale). Troca aceita: todo container da `proxy_net` alcança o agens, e o agens alcança todos; a proteção é a assinatura HMAC por agente na entrada e o token na rota de admin. Alternativa descartada: rede própria `agens_net`, em que cada sistema entraria explicitamente |

### Fechada — avisos do sistema (26/10/2026, com o Sérgio)

| # | Decisão | Consequência |
|---|---|---|
| A11 | **O sistema continua dono dos lembretes e notificações e avisa o agens pelo eco.** O Eliza pré-registra cada envio automático (org + hash do texto) antes de mandar; quando o eco `deMim` volta, encaminha com `aviso: { tipo }`. O agens guarda o texto na memória (papel `aviso`), **sem pausar** e sem responder | Corrige o lembrete pausando a conversa por 12 h como se fosse a equipe (achado no 1º teste em produção, 26/10). Contrato: `Eliza/docs/contratos/autoatendimento/09-avisos-do-sistema.md` (C22–C27, lado do agens na §7). Descartado: o agens assumir os lembretes (exigiria iniciar conversa sem ticket, e orgs sem o add-on continuam precisando do cron). Vale para os próximos sistemas: quem envia mensagem automática pelo mesmo número marca o envio da mesma forma |

## 11. Módulo de treinamento (rascunho — B4, rediscutir antes de construir)

> **Em rediscussão na rodada 5 (27/10/2026):** [docs/RODADA-5-TREINAMENTO.md](docs/RODADA-5-TREINAMENTO.md)
> (T1–T12). O pedido muda dois pontos deste rascunho: aprimoramento **automático** de estilo (T5) e
> o formato de ensaio (T3). Até a rodada fechar, este rascunho vale só como histórico.

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
