// Package mensagem tem os tipos de domínio da mensagem normalizada (specs/01 §5).
//
// DECISÃO: pacote próprio e pequeno (sem dependências) para que entrada, agent
// e store possam importá-lo sem ciclo.
package mensagem

import "time"

// Mensagem de entrada já normalizada pelo modo de entrada.
type Mensagem struct {
	ID           string // id no canal (WhatsApp: mensagemId)
	RecebidaEm   time.Time
	Texto        string // vazio quando só há anexo
	Anexos       []Anexo
	NomeExibicao string // pushName; só conveniência, nunca identidade

	// Ticket é o da requisição que trouxe ESTA mensagem (vazio no deMim). O
	// ticket é por mensagem: baixarMidia de um áudio da rajada usa o do próprio
	// áudio. DECISÃO (fase 5): o ticket viaja dentro da Mensagem, assim entrada,
	// debounce e Turno guardam um por mensagem sem mudar de assinatura.
	Ticket string
}

// Anexo de uma mensagem. O agens nunca guarda o arquivo.
type Anexo struct {
	Tipo        string // audio | imagem | documento | video | figurinha | outro
	Mime        string
	Tamanho     int64 // bytes; 0 se o canal não informou
	DuracaoS    int   // só áudio/vídeo
	Encaminhado bool
	Ref         string // como pedir o arquivo ao canal/sistema

	// TipoOriginal guarda o tipo que o canal informou quando ele não é
	// suportado (Tipo = "outro"). DECISÃO: campo extra, pedido na fase 1.
	TipoOriginal string
}
