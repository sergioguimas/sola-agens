//go:build windows

package main

import "os"

// DECISÃO: no Windows não existe SIGHUP em syscall; a recarga por sinal é
// no-op (produção roda em container Linux; para recarregar aqui, reinicie).
func sinaisRecarga() []os.Signal { return nil }
