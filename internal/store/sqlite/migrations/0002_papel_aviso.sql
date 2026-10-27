-- 0002_papel_aviso.sql
-- Papel 'aviso': envio automático do sistema (lembrete, notificação) guardado
-- como contexto, sem pausar a conversa (A11, contrato 09 do Eliza §7).
-- O CHECK não se altera de forma portável: a tabela é reconstruída (B1).
CREATE TABLE mensagens_nova (
  id         TEXT PRIMARY KEY,
  agente     TEXT NOT NULL,
  tenant     TEXT NOT NULL,
  contato    TEXT NOT NULL,
  papel      TEXT NOT NULL CHECK (papel IN ('cliente','agente','humano','aviso')),
  conteudo   TEXT,                    -- NULL depois da retenção
  anexos     TEXT,                    -- JSON de []Anexo sem Ref
  criada_em  INTEGER NOT NULL
);
INSERT INTO mensagens_nova (id, agente, tenant, contato, papel, conteudo, anexos, criada_em)
  SELECT id, agente, tenant, contato, papel, conteudo, anexos, criada_em FROM mensagens;
DROP TABLE mensagens;
ALTER TABLE mensagens_nova RENAME TO mensagens;
CREATE INDEX ix_mensagens_conversa ON mensagens (agente, tenant, contato, criada_em);
CREATE INDEX ix_mensagens_criada ON mensagens (criada_em);
