# Rodada 5 — modo de treinamento (o tenant ensina o agens a falar como ele)

> **Status:** rodada de 27/10/2026 com o Sérgio, depois do 1º atendimento real da F1. **T1–T12 fechadas,
> todas pela recomendação** (T5, T6, T9 e T12 escolhidas explicitamente; as demais confirmadas como
> propostas). Contrato do agens: [specs/06-treinamento.md](../specs/06-treinamento.md).
> Parte do rascunho da CONTRATO §11 (B4: "aceito como direção, rediscutir em detalhe antes de construir").
> Cada decisão traz a recomendação, a alternativa descartada e o motivo. Marcadas **🟡 em aberto**: as
> que o pedido muda em relação ao rascunho ou que dependem de escolha de negócio. **🟢 proposta**: segue
> o rascunho ou é consequência direta; vale se o Sérgio não contestar.
> **Depois desta rodada:** contrato do agens (`specs/06-treinamento.md`, TO-BE) e contrato do lado do
> Eliza (`Eliza/docs/contratos/autoatendimento/09-*`), nessa ordem.

## O pedido

Cada tenant (a dona do salão, no Eliza) tem uma **conversa interna** com o agens e o **ensina a falar com
os clientes**. O agens **anota os padrões** do jeito dela escrever (palavras, pontuação, emojis, tom) e
**vai se aprimorando** a cada conversa.

## O que é (em uma linha)

Um segundo modo do motor, em que o interlocutor é o **operador** do tenant, não o cliente: o agens conduz
um treino, extrai do que o operador escreve um **perfil de estilo** estruturado e versionado, e esse
perfil entra no prompt de atendimento daquele tenant.

```txt
painel do sistema (operador logado)      sistema (ex.: Eliza)                   sola-agens
 chat de treino ──POST /api/.../treino──▶ papel owner/admin, asserção do operador
                                          ──POST /a/eliza/v1/treino (assinado)──▶ turno síncrono, modo treino
                                                                                  ├─ conduz o treino (simula cliente)
                                                                                  └─ ao fechar a sessão: extração
                                                                                       └─ perfil v(N+1)
 ◀── { resposta } ───────────────────────◀── { resposta } ─────────────────────┘

atendimento normal (F1): prompt = motor → sistema → tenant (A4) + ESTILO DO ESTABELECIMENTO (perfil ativo)
```

**Não é fine-tuning nem memória livre.** O que muda o comportamento é um JSON pequeno, legível e
versionado; dá para mostrar, comparar e desfazer.

## Decisões

| # | Decisão (recomendação) | Alternativa descartada | Motivo |
|---|---|---|---|
| T1 ✅ | **Canal: chat no painel do sistema** (web, operador logado). Sem WhatsApp | Treinar pelo WhatsApp do próprio número | No modo encaminhado, tudo o que sai do número da clínica é `deMim` e pausa o atendimento; um número de treino separado é o "segundo aparelho" que o Sérgio quer evitar. O painel já tem login e papel |
| T2 ✅ | **Identidade: asserção HMAC do sistema** com `tenant`, `operadorId`, `papel` e validade curta. Só **owner/admin** treinam (no Eliza) | Ticket do cliente; qualquer papel | Cliente final nunca entra nesse modo (§11). O perfil muda como o bot fala com todos os clientes da org: é decisão de dono, não de profissional ou recepção |
| T3 ✅ | **Formato do treino: ensaio.** O agens faz o papel de cliente ("oi, tem horário sábado?", "quanto é a unha em gel?", "preciso desmarcar") e o operador **responde como responderia de verdade**. Entre um ensaio e outro, o agens pergunta o que não dá para ver nas respostas ("trata por você ou senhora?", "tem emoji que você nunca usa?") | Só questionário ("descreva seu tom") | Ninguém descreve bem o próprio jeito de escrever; ele aparece quando a pessoa responde. As respostas do ensaio são a matéria-prima dos padrões que o Sérgio pediu (palavras, pontuação, emojis) e viram exemplos reais para o prompt |
| T4 ✅ | **O que é aprendido: perfil de estilo estruturado**, campos com limite (esquema abaixo). Cada item guarda a **evidência** (quantas vezes apareceu, em quantas sessões) | Texto livre ("a dona fala de forma carinhosa…"); guardar a conversa toda e jogar no prompt | Estrutura deixa limitar, mostrar e comparar versões, e protege o prompt de injeção. Texto livre cresce sem controle e mistura estilo com promessa. A conversa inteira custaria tokens em todo atendimento |
| T5 ✅ | **Aprimoramento automático só de estilo, com versão e volta.** Ao fechar cada sessão, a extração gera a versão N+1 e **ela entra em vigor sozinha** quando só mexe em estilo (tratamento, saudação, emoji, pontuação, comprimento, vocabulário, exemplos). Item novo só entra com evidência em **2 sessões** ou se o operador confirmar no chat ("isso, sempre assim"). O painel mostra o que mudou e tem "voltar à versão anterior". **Conteúdo com afirmação de fato** ("domingo a gente abre", "pagamento só no Pix", "primeira vez tem desconto") **nunca entra sozinho**: vira "nota para revisar" e só vale aprovada | (a) Tudo por aprovação, como no rascunho da §11; (b) tudo automático | (a) cumpre a segurança, mas não é o "se auto-aprimorar" pedido: o perfil para de evoluir quando ninguém abre o painel. (b) deixa uma frase solta do treino virar promessa feita a cliente real. Estilo errado é estranho, mas não gera compromisso; fato errado gera. O limiar de 2 sessões evita que uma resposta atípica mude o bot |
| T6 ✅ | **Fonte do aprendizado na v1: só a conversa de treino** (e as correções do operador nela) | Aprender também das respostas reais da equipe aos clientes (`deMim`) | Respostas reais são a fonte mais rica e fariam o aprimoramento acontecer sem ninguém treinar. Mas põem texto de cliente final (nome, pedido, às vezes saúde) numa extração por LLM e num perfil que dura mais que os 90 dias de retenção: exige decisão de LGPD própria (anonimizar antes, base legal, o que guardar). Fica como **v2**, com rodada própria |
| T7 ✅ | **Onde fica: base do agens**, por `agente + tenant`: `perfis` (versões, uma ativa), `sessoes_treino`, `mensagens_treino`, `observacoes` (evidência). Exceção à A4 já prevista na §11 (é dado que o agens produz). O sistema lê e manda pela API de treino | Guardar no banco do Eliza | O agens é de todos os sistemas; cada sistema reimplementar o perfil repetiria a lógica de versão e evidência. O sistema continua dono da configuração que **ele** decide (persona, respostas fixas, política) |
| T8 ✅ | **No prompt: seção `ESTILO DO ESTABELECIMENTO`** depois das instruções do tenant e **antes** da `REGRA DE SEGURANÇA` (que continua fixa no código e por último na ordem de força). Teto de **~500 tokens**; os exemplos entram marcados como dado ("assim a equipe costuma responder; imite o jeito, não copie fatos") | Perfil no lugar da persona/tom; sem teto | Estilo é a camada mais fraca: não libera ferramenta nem muda regra. O teto mantém o custo (+~500 tokens/turno ≈ US$ 0,0004 hoje) e evita que o estilo afogue as regras |
| T9 ✅ | **Precedência com a config do sistema:** quando existe perfil ativo, ele **substitui o `tom`** do `/contexto`; `persona` (nome do atendente), respostas fixas do fastpath e política continuam vindo do sistema | (a) Config do sistema sempre vence o perfil; (b) o perfil reescreve também as respostas fixas | `tom` em uma linha ("cordial, direto") é a versão pobre do que o perfil captura; manter os dois gera instrução contraditória. As respostas fixas precisam ser determinísticas (fastpath, S4): reescrevê-las com o perfil fica para depois, como sugestão aprovada |
| T10 ✅ | **Prévia antes de valer:** no painel, o operador escreve como cliente e vê a resposta do bot **com o perfil novo**, usando o catálogo real da org **só para leitura** (sem gravar, sem WhatsApp) | Perfil novo valer direto, sem prévia | É a forma de o operador conferir "agora sim, é meu jeito" antes do cliente ver, e de pegar uma mudança automática ruim (T5) |
| T11 ✅ | **Extração com saída estruturada** (`responseSchema` do Gemini, JSON validado no motor): o modelo devolve observações por campo; o motor faz a fusão, conta evidência, aplica os limites e decide o que é estilo e o que é fato (T5). Modelo padrão (3.6 Flash, A9) | O modelo reescrever o perfil inteiro a cada sessão | Fusão no código é testável e não "esquece" o que já foi aprendido a cada rodada. Reescrita inteira faz o perfil oscilar |
| T12 ✅ | **Ordem: depois da F2a** (chat da `/marcar`), reaproveitando o turno síncrono `{ resposta, acoes[] }` da W4 e a UI React da W9. Fase nova **F2c** (treinamento), piloto na Clínica Admin | Treinamento antes da F2a | A F2a já está toda decidida e constrói exatamente o que o treino precisa: rota síncrona no agens e chat React no Eliza. Fazer o treino primeiro obriga a construir essa base agora e adaptar a F2a depois. Se o treino for mais urgente para o negócio que o chat da página pública, inverter custa pouco: só muda quem constrói a base |

## Perfil de estilo (esboço do esquema; fecha no contrato)

| Campo | Exemplo | Limite |
|---|---|---|
| `tratamento` | `voce` \| `senhor` \| `pelo_nome` | 1 |
| `saudacoes` / `despedidas` | "Oii, tudo bem? 💅", "Beijos, até sábado!" | 5 cada, 80 caracteres |
| `emojis.uso` | `nunca` \| `pouco` \| `frequente` | 1 |
| `emojis.preferidos` / `emojis.evitar` | 💅 ✨ / 😂 | 8 / 8 |
| `pontuacao` | exclamação `rara`\|`normal`\|`muita`; reticências sim/não; "kkk" sim/não | — |
| `comprimento` | `curto` \| `medio` | 1 |
| `vocabulario.preferir` | "horário" em vez de "agendamento", "flor" como vocativo | 15, 40 caracteres |
| `vocabulario.evitar` | "prezada", "informamos" | 15 |
| `tom` | carinhosa, informal, animada | 5 descritores |
| `exemplos` | `{ situacao: "cliente pede encaixe", resposta: "Ai amiga, sábado tá lotadinho 😩 mas sexta 16h eu tenho!" }` | 8 pares, 300 caracteres |
| `notas_para_revisar` | "disse que domingo abre" (fato, T5: não entra no prompt sem aprovação) | 20 |

Cada item carrega `evidencia: { ocorrencias, sessoes, ultima_sessao }` e `origem: extraido | confirmado`.

## Segurança

- **Injeção pelo próprio operador:** o perfil entra no prompt de sistema, então o texto passa por
  limpeza e limites por campo (como o `nomeSeguro` da fase 4) e os exemplos entram como dado. Ordem do
  tipo "ignore as regras" vinda do treino é descartada pela extração e registrada.
- **O perfil nunca:** libera ferramenta, muda política (horário, preço, antecedência), altera a
  `REGRA DE SEGURANÇA` ou faz o bot afirmar fato do negócio (T5).
- **Escopo:** perfil, sessões e observações são sempre por `agente + tenant`; nenhum tenant lê ou
  influencia o de outro.
- **Retenção:** mensagens do treino seguem os 90 dias (A-retenção); o perfil e as observações não
  guardam texto de cliente final na v1 (T6).

## Fica de fora desta rodada

Aprender das respostas reais da equipe (`deMim`, T6 → v2 com LGPD); estilo nas respostas fixas do
fastpath (T9); treino por áudio; perfil por profissional (hoje é por tenant); widget de treino genérico
para sistemas sem painel (F2b).

## Fechamento

Fechada em 27/10/2026: T5 (automático só de estilo, fato nunca sozinho), T6 (v1 só a conversa de
treino), T9 (perfil substitui o `tom`) e T12 (depois da F2a, fase F2c) pela recomendação; T1–T4,
T7, T8, T10 e T11 confirmadas como propostas.
