package main

import (
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestVerificarSaude(t *testing.T) {
	cli := &http.Client{Timeout: time.Second}
	para := func(status int) string {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path != "/health" {
				w.WriteHeader(404)
				return
			}
			w.WriteHeader(status)
		}))
		t.Cleanup(srv.Close)
		_, porta, _ := net.SplitHostPort(srv.Listener.Addr().String())
		return "0.0.0.0:" + porta
	}
	if err := verificarSaude(para(200), cli); err != nil {
		t.Errorf("200: %v", err)
	}
	if err := verificarSaude(para(503), cli); err == nil {
		t.Error("503: esperava erro")
	}
	if err := verificarSaude("0.0.0.0:1", cli); err == nil { // porta sem servidor
		t.Error("sem servidor: esperava erro")
	}
	if err := verificarSaude("sem-porta", cli); err == nil {
		t.Error("listen inválido: esperava erro")
	}
}
