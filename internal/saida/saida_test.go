package saida

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestClienteNaoSegueRedirectParaOutroHost(t *testing.T) {
	var ticket string
	var chamadas int
	alvo := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		chamadas++
		ticket = r.Header.Get("X-Ticket")
	}))
	defer alvo.Close()
	origem := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, alvo.URL, http.StatusFound)
	}))
	defer origem.Close()

	req, _ := http.NewRequest(http.MethodGet, origem.URL, nil)
	req.Header.Set("X-Ticket", "segredo")
	resp, err := Cliente(0).Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, quer 302", resp.StatusCode)
	}
	if chamadas != 0 || ticket != "" {
		t.Fatalf("redirect seguido para outro host (%d chamadas, ticket %q)", chamadas, ticket)
	}
}

func TestClienteSegueRedirectNaMesmaOrigem(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/spec" {
			http.Redirect(w, r, "/spec/", http.StatusPermanentRedirect)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	resp, err := Cliente(0).Get(srv.URL + "/spec")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, quer 200 após o redirect", resp.StatusCode)
	}
}

func TestClienteLimitaRedirects(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/loop", http.StatusFound)
	}))
	defer srv.Close()

	resp, err := Cliente(0).Get(srv.URL + "/loop")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, quer 302 após o limite", resp.StatusCode)
	}
}
