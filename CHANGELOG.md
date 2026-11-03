# Changelog

Todas as mudanças notáveis deste projeto são documentadas neste arquivo.

O formato é baseado em [Mantenha um Changelog](https://keepachangelog.com/pt-BR/1.1.0/),
e este projeto adere ao [Versionamento Semântico](https://semver.org/lang/pt-BR/spec/v2.0.0.html).

## [Não publicado]

### Corrigido

- Respostas do Gemini e do Groq agora têm limite de tamanho (8 MB e 1 MB). Antes eram lidas sem teto, e uma resposta anômala podia ocupar a memória do processo que atende todas as conversas.

### Segurança

- Compila com Go 1.26.10, que elimina as 28 vulnerabilidades da biblioteca padrão alcançáveis pelo código na versão 1.26.0. A imagem de build deixa de usar tag flutuante, e a imagem final passa para `distroless/static-debian13`, fixada por digest.
- Redirect para outro host, esquema ou porta não é mais seguido nas chamadas de saída (sistema, OpenAPI, Gemini, Groq). O ticket do cliente, os headers fixos e a `x-goog-api-key` não vazam mais para terceiros, e o sistema que redirecionar para fora tem a chamada abortada.
- O segredo HMAC da entrada e o `AGENS_ADMIN_TOKEN` precisam de no mínimo 32 caracteres. Abaixo disso o processo não sobe e diz qual variável corrigir; o token vazio continua desligando a rota de apagamento.
- O mimetype do áudio é validado antes de virar cabeçalho do multipart do Groq. Um valor inválido, inclusive com quebra de linha, falha a transcrição antes de sair qualquer chamada.
- Dependências atualizadas: SQLite embutida 3.54.1 (`modernc.org/sqlite` v1.61.1), `x/text` v0.43.0 e `x/sys` v0.49.0. Nenhuma tinha CVE conhecido.
- O YAML dos agentes passa a ser lido com `go.yaml.in/yaml/v3`, continuação mantida do `gopkg.in/yaml.v3`, que foi arquivado sem manutenção. O formato dos arquivos não muda.

## [1.1.0] - 2026-11-02

### Modificado

- Pausa por intervenção humana passa de 12 h para **4 h** por padrão (`AGENS_PAUSA_HUMANA_HORAS`). Com 12 h, o cliente atendido à mão de manhã ficava sem o agente à tarde. Quem já tem `AGENS_PAUSA_HUMANA_HORAS=12` no `.env` precisa trocar o valor ou apagar a linha.

## [1.0.0] - 2026-11-02

### Adicionado

- Plataforma interna de atendente (bot LLM) da Sola em Go, com o Eliza como primeiro sistema: lê a OpenAPI do sistema em tempo de execução, atende por WhatsApp em texto e áudio, identifica o cliente pelo ticket do sistema, confirma e cancela pelo fastpath, pausa quando a equipe responde pelo celular e trata avisos do sistema (lembretes) sem pausar.
- Multi-tenant (agente + tenant) com configuração em camadas (motor → sistema → tenant), store SQLite com retenção de 90 dias e bateria de avaliação com Gemini real.

[Não publicado]: https://github.com/sergioguimas/sola-agens/compare/v1.1.0...HEAD
[1.1.0]: https://github.com/sergioguimas/sola-agens/compare/v1.0.0...v1.1.0
[1.0.0]: https://github.com/sergioguimas/sola-agens/releases/tag/v1.0.0
