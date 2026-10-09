package main

import (
	"fmt"
	"net"
	"net/http"
	"os"
	"time"
)

// saude é o subcomando `sola-agens health` (HEALTHCHECK do container: a imagem
// distroless não tem shell nem curl). Não carrega .env nem a config: só lê
// AGENS_LISTEN para achar a porta e faz GET em 127.0.0.1:<porta>/health.
func saude() int {
	listen := os.Getenv("AGENS_LISTEN")
	if listen == "" {
		listen = "0.0.0.0:4100"
	}
	if err := verificarSaude(listen, &http.Client{Timeout: 3 * time.Second}); err != nil {
		fmt.Fprintln(os.Stderr, "health:", err)
		return 1
	}
	return 0
}

func verificarSaude(listen string, cli *http.Client) error {
	_, porta, err := net.SplitHostPort(listen)
	if err != nil || porta == "" {
		return fmt.Errorf("AGENS_LISTEN inválido")
	}
	resp, err := cli.Get("http://" + net.JoinHostPort("127.0.0.1", porta) + "/health")
	if err != nil {
		return fmt.Errorf("sem resposta")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("status %d", resp.StatusCode)
	}
	return nil
}
