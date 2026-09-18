package mk

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNewRejectsIncompleteConfiguration(t *testing.T) {
	cases := []struct {
		name, baseURL, inputToken, password string
	}{
		{"sem base", "", "token", "senha"},
		{"sem token", "https://mk.example.com", "", "senha"},
		{"sem senha", "https://mk.example.com", "token", ""},
		{"base inválida", "not-a-url", "token", "senha"},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			if _, err := New(testCase.baseURL, testCase.inputToken, testCase.password, http.DefaultClient); err == nil {
				t.Fatal("configuração incompleta foi aceita")
			}
		})
	}
}

func TestTokenSendsConfirmedAuthenticationContract(t *testing.T) {
	var receivedQuery string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			t.Errorf("method = %s", r.Method)
		}
		if r.URL.Path != "/mk/WSAutenticacao.rule" {
			t.Errorf("path = %s", r.URL.Path)
		}
		receivedQuery = r.URL.RawQuery
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Token":"temp-token-123","Expire":"2026-01-01"}`))
	}))
	defer server.Close()

	client, err := New(server.URL, "input-token", "input-password", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	token, err := client.Token(context.Background(), "6")
	if err != nil {
		t.Fatal(err)
	}
	if token != "temp-token-123" {
		t.Errorf("token = %q", token)
	}
	query := receivedQuery
	if !strings.Contains(query, "sys=MK0") || !strings.Contains(query, "token=input-token") ||
		!strings.Contains(query, "password=input-password") || !strings.Contains(query, "cd_servico=6") {
		t.Errorf("query = %q", query)
	}
}

func TestTokenFindsFieldRegardlessOfCasingOrNesting(t *testing.T) {
	cases := []struct {
		name, body string
	}{
		{"campo simples", `{"Token":"abc"}`},
		{"campo com nome alternativo", `{"TokenAutenticacao":"abc"}`},
		{"campo aninhado", `{"retorno":{"TokenRetornoAutenticacao":"abc"}}`},
	}
	for _, testCase := range cases {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(testCase.body))
			}))
			defer server.Close()

			client, err := New(server.URL, "token", "senha", server.Client())
			if err != nil {
				t.Fatal(err)
			}
			token, err := client.Token(context.Background(), "6")
			if err != nil {
				t.Fatal(err)
			}
			if token != "abc" {
				t.Errorf("token = %q", token)
			}
		})
	}
}

func TestTokenFailsWithoutRecognizableField(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ERRO","mensagem":"credenciais inválidas"}`))
	}))
	defer server.Close()

	client, err := New(server.URL, "token", "senha", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := client.Token(context.Background(), "6"); err == nil {
		t.Fatal("resposta sem token foi aceita")
	}
}

func TestTokenCachesUntilInvalidated(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Token":"cached-token"}`))
	}))
	defer server.Close()

	client, err := New(server.URL, "token", "senha", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := client.Token(ctx, "6"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Token(ctx, "6"); err != nil {
		t.Fatal(err)
	}
	if calls != 1 {
		t.Fatalf("chamadas ao MK = %d, esperava 1 (token deveria estar em cache)", calls)
	}

	client.InvalidateToken("6")
	if _, err := client.Token(ctx, "6"); err != nil {
		t.Fatal(err)
	}
	if calls != 2 {
		t.Fatalf("chamadas ao MK = %d, esperava 2 após invalidar o cache", calls)
	}
}

func TestTokenCachedSeparatelyPerServiceCode(t *testing.T) {
	var queries []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		queries = append(queries, r.URL.RawQuery)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"Token":"token-for-this-service"}`))
	}))
	defer server.Close()

	client, err := New(server.URL, "token", "senha", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := client.Token(ctx, "6"); err != nil {
		t.Fatal(err)
	}
	if _, err := client.Token(ctx, "38"); err != nil {
		t.Fatal(err)
	}
	if len(queries) != 2 {
		t.Fatalf("chamadas ao MK = %d, esperava 2 (cd_servico diferentes não compartilham cache)", len(queries))
	}
}

func TestErrorNeverLeaksCredentialsOrURL(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	unreachableURL := server.URL
	server.Close() // fecha antes de qualquer requisição: força erro de conexão.

	client, err := New(unreachableURL, "secret-input-token", "secret-password", http.DefaultClient)
	if err != nil {
		t.Fatal(err)
	}
	_, tokenErr := client.Token(context.Background(), "6")
	if tokenErr == nil {
		t.Fatal("esperava erro de conexão")
	}
	message := tokenErr.Error()
	if strings.Contains(message, "secret-input-token") || strings.Contains(message, "secret-password") {
		t.Fatalf("mensagem de erro vazou credenciais: %q", message)
	}
	if strings.Contains(message, "WSAutenticacao") || strings.Contains(message, unreachableURL) {
		t.Fatalf("mensagem de erro vazou a URL da requisição: %q", message)
	}
}

// TestListServiceTypesParsesConfirmedRealShape usa o formato exato observado
// contra o MK de produção em 2026-09-18 (sac.newlifefibra.com.br), reduzido a
// duas entradas.
func TestListServiceTypesParsesConfirmedRealShape(t *testing.T) {
	var authCalls, listCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/mk/WSAutenticacao.rule":
			authCalls++
			_, _ = w.Write([]byte(`{"Expire":"20/09/2026 11:56:54","LimiteUso":0,"ServicosAutorizados":[1,2,3],"Token":"temp-token","status":"OK"}`))
		case "/mk/WSMKOSListaTiposOS.rule":
			listCalls++
			if got := r.URL.Query().Get("token"); got != "temp-token" {
				t.Errorf("token usado na listagem = %q", got)
			}
			_, _ = w.Write([]byte(`{"Tipos": [{"codostipo": 93, "descricao": "BAIXA SETOR RURAL"}, {"codostipo": 29, "descricao": "INSTALAÇÃO RURAL"}], "status": "OK"}`))
		default:
			t.Errorf("path inesperado: %s", r.URL.Path)
		}
	}))
	defer server.Close()

	client, err := New(server.URL, "token", "senha", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	types, err := client.ListServiceTypes(context.Background(), "9999")
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 2 || types[0].Code != 93 || types[0].Description != "BAIXA SETOR RURAL" || types[1].Code != 29 {
		t.Fatalf("types = %+v", types)
	}
	if authCalls != 1 || listCalls != 1 {
		t.Fatalf("authCalls=%d listCalls=%d", authCalls, listCalls)
	}
}

func TestListServiceTypesReauthenticatesOnceOn401(t *testing.T) {
	var authCalls, listCalls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/mk/WSAutenticacao.rule":
			authCalls++
			_, _ = w.Write([]byte(`{"Token":"temp-token","status":"OK"}`))
		case "/mk/WSMKOSListaTiposOS.rule":
			listCalls++
			if listCalls == 1 {
				w.WriteHeader(http.StatusUnauthorized)
				return
			}
			_, _ = w.Write([]byte(`{"Tipos": [{"codostipo": 29, "descricao": "INSTALAÇÃO RURAL"}], "status": "OK"}`))
		}
	}))
	defer server.Close()

	client, err := New(server.URL, "token", "senha", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	types, err := client.ListServiceTypes(context.Background(), "9999")
	if err != nil {
		t.Fatal(err)
	}
	if len(types) != 1 || types[0].Code != 29 {
		t.Fatalf("types = %+v", types)
	}
	if authCalls != 2 || listCalls != 2 {
		t.Fatalf("esperava reautenticar uma vez: authCalls=%d listCalls=%d", authCalls, listCalls)
	}
}
