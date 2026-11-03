-- 0003_treino.sql
-- Modo de treinamento (specs/06 §10): sessões, mensagens, observações, perfis
-- versionados e notas de fato, por agente + tenant. O schema entra inteiro aqui
-- mesmo com a F2c em fases: migration aplicada não muda depois.
CREATE TABLE sessoes_treino (
  id            TEXT PRIMARY KEY,
  agente        TEXT NOT NULL,
  tenant        TEXT NOT NULL,
  operador_id   TEXT NOT NULL,
  estado        TEXT NOT NULL CHECK (estado IN ('aberta','encerrada','extraida','erro_extracao')),
  roteiro_json  TEXT NOT NULL,        -- {"feitos":{"preco":2},"preferencias":[]}
  versao_gerada INTEGER,              -- NULL = não gerou versão
  aberta_em     INTEGER NOT NULL,
  ultima_msg_em INTEGER NOT NULL,
  encerrada_em  INTEGER
);
CREATE INDEX ix_sessoes_tenant ON sessoes_treino (agente, tenant, aberta_em);
CREATE INDEX ix_sessoes_abertas ON sessoes_treino (estado, ultima_msg_em);

CREATE TABLE mensagens_treino (
  id         TEXT PRIMARY KEY,
  sessao_id  TEXT NOT NULL,
  papel      TEXT NOT NULL CHECK (papel IN ('operador','agente')),
  tipo       TEXT NOT NULL CHECK (tipo IN ('livre','ensaio','resposta_ensaio')),
  cenario    TEXT,
  conteudo   TEXT,                    -- NULL depois da retenção (90 dias)
  criada_em  INTEGER NOT NULL
);
CREATE INDEX ix_msg_treino_sessao ON mensagens_treino (sessao_id, criada_em);
CREATE INDEX ix_msg_treino_criada ON mensagens_treino (criada_em);

CREATE TABLE observacoes (
  id             TEXT PRIMARY KEY,
  agente         TEXT NOT NULL,
  tenant         TEXT NOT NULL,
  campo          TEXT NOT NULL,
  valor          TEXT NOT NULL,       -- normalizado
  valor_exibicao TEXT NOT NULL,
  cenario        TEXT,                -- só em exemplos
  origem         TEXT NOT NULL CHECK (origem IN ('extraida','declarada','metrica')),
  estado         TEXT NOT NULL CHECK (estado IN ('candidata','rejeitada')),
  ocorrencias    INTEGER NOT NULL,
  sessoes        INTEGER NOT NULL,
  ultima_sessao  TEXT NOT NULL,
  citacao        TEXT,                -- até 200 caracteres, do operador
  criada_em      INTEGER NOT NULL,
  atualizada_em  INTEGER NOT NULL,
  UNIQUE (agente, tenant, campo, valor)
);

CREATE TABLE perfis (
  agente     TEXT NOT NULL,
  tenant     TEXT NOT NULL,
  versao     INTEGER NOT NULL,
  ativa      INTEGER NOT NULL CHECK (ativa IN (0,1)),
  perfil     TEXT NOT NULL,           -- JSON validado (specs/06 §8)
  diff       TEXT NOT NULL,           -- JSON [{campo,antes,depois}]
  origem     TEXT NOT NULL,           -- sessao:{id} | volta:{n} | edicao | zerado
  criada_por TEXT NOT NULL,           -- operador_id
  criada_em  INTEGER NOT NULL,
  PRIMARY KEY (agente, tenant, versao)
);

CREATE TABLE notas (
  id           TEXT PRIMARY KEY,
  agente       TEXT NOT NULL,
  tenant       TEXT NOT NULL,
  sessao_id    TEXT NOT NULL,
  texto        TEXT NOT NULL,         -- até 300 caracteres
  citacao      TEXT,
  estado       TEXT NOT NULL CHECK (estado IN ('pendente','aplicada','descartada')),
  criada_em    INTEGER NOT NULL,
  resolvida_em INTEGER
);
CREATE INDEX ix_notas_tenant ON notas (agente, tenant, estado);

-- turnos.modo ganha 'treino' e 'previa'. O CHECK não se altera de forma
-- portável: a tabela é reconstruída, como na 0002 (B1).
CREATE TABLE turnos_nova (
  id              TEXT PRIMARY KEY,
  agente          TEXT NOT NULL,
  tenant          TEXT NOT NULL,
  contato         TEXT NOT NULL,
  modo            TEXT NOT NULL CHECK (modo IN ('llm','fastpath','fixa','pausado','erro','treino','previa')),
  mensagem_ids    TEXT NOT NULL,      -- JSON []string
  modelo          TEXT,
  iteracoes       INTEGER NOT NULL DEFAULT 0,
  ferramentas     TEXT NOT NULL,      -- JSON []{nome,status,codigo}
  tokens_entrada  INTEGER NOT NULL DEFAULT 0,
  tokens_saida    INTEGER NOT NULL DEFAULT 0,
  custo_micro_usd INTEGER NOT NULL DEFAULT 0,
  latencia_ms     INTEGER NOT NULL DEFAULT 0,
  resultado       TEXT NOT NULL,
  escalou         INTEGER NOT NULL DEFAULT 0 CHECK (escalou IN (0,1)),
  criado_em       INTEGER NOT NULL
);
INSERT INTO turnos_nova (id, agente, tenant, contato, modo, mensagem_ids, modelo, iteracoes, ferramentas,
    tokens_entrada, tokens_saida, custo_micro_usd, latencia_ms, resultado, escalou, criado_em)
  SELECT id, agente, tenant, contato, modo, mensagem_ids, modelo, iteracoes, ferramentas,
    tokens_entrada, tokens_saida, custo_micro_usd, latencia_ms, resultado, escalou, criado_em FROM turnos;
DROP TABLE turnos;
ALTER TABLE turnos_nova RENAME TO turnos;
CREATE INDEX ix_turnos_agente_tenant ON turnos (agente, tenant, criado_em);
