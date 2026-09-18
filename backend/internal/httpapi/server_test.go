package httpapi

import (
	"bytes"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestProtectedRouteRejectsMissingTokenBeforeStore(t *testing.T) {
	server := &Server{apiToken: "123456789012345678901234", logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	handler := server.auth(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { t.Fatal("handler não deveria ser chamado") }))
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodGet, "/api/v1/dashboard", nil))
	if recorder.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d", recorder.Code)
	}
}

func TestParseOperationalDate(t *testing.T) {
	valid, err := parseOperationalDate("2026-09-17")
	if err != nil || valid.Format(time.DateOnly) != "2026-09-17" {
		t.Fatalf("data válida rejeitada: %v, %v", valid, err)
	}
	for _, value := range []string{"", "17/09/2026", "2026-9-17", "2026-02-30", "2026-09-17T00:00:00Z"} {
		if _, err := parseOperationalDate(value); err == nil {
			t.Errorf("data inválida aceita: %q", value)
		}
	}
}

func TestPositiveQueryInt(t *testing.T) {
	for _, test := range []struct {
		query string
		want  int
		valid bool
	}{{"", 25, true}, {"?page=2", 2, true}, {"?page=0", 0, false}, {"?page=x", 0, false}, {"?page=201", 0, false}} {
		request := httptest.NewRequest(http.MethodGet, "/orders"+test.query, nil)
		got, err := positiveQueryInt(request, "page", 25, 200)
		if (err == nil) != test.valid || (test.valid && got != test.want) {
			t.Errorf("query %q: got=%d err=%v", test.query, got, err)
		}
	}
}

func TestProtectedRouteAcceptsToken(t *testing.T) {
	const token = "123456789012345678901234"
	server := &Server{apiToken: token, logger: slog.New(slog.NewTextHandler(io.Discard, nil))}
	called := false
	handler := server.auth(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { called = true; w.WriteHeader(http.StatusNoContent) }))
	request := httptest.NewRequest(http.MethodGet, "/api/v1/dashboard", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if !called || recorder.Code != http.StatusNoContent {
		t.Fatalf("called=%v status=%d", called, recorder.Code)
	}
}

func TestEveryAPIRouteRequiresAuthentication(t *testing.T) {
	handler := New(nil, "simulation", "123456789012345678901234", slog.New(slog.NewTextHandler(io.Discard, nil)))
	requests := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/dashboard"},
		{http.MethodGet, "/api/v1/orders"},
		{http.MethodPut, "/api/v1/orders/id/location"},
		{http.MethodGet, "/api/v1/operations"},
		{http.MethodGet, "/api/v1/service-types"},
		{http.MethodGet, "/api/v1/teams"},
		{http.MethodPut, "/api/v1/operations/id/service-types"},
		{http.MethodPost, "/api/v1/sync-runs"},
		{http.MethodGet, "/api/v1/sync-runs/latest"},
		{http.MethodGet, "/api/v1/planning-proposals"},
		{http.MethodPost, "/api/v1/planning-proposals"},
		{http.MethodPut, "/api/v1/planning-proposals/id"},
		{http.MethodPost, "/api/v1/planning-proposals/id/refresh"},
		{http.MethodPost, "/api/v1/planning-proposals/id/approve"},
		{http.MethodGet, "/api/v1/planning-proposals/id/scheduling-preview"},
	}
	for _, item := range requests {
		t.Run(item.method+" "+item.path, func(t *testing.T) {
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(item.method, item.path, nil))
			if recorder.Code != http.StatusUnauthorized {
				t.Fatalf("status = %d", recorder.Code)
			}
		})
	}
}

func TestDecodeJSONAcceptsOneDocumentOnly(t *testing.T) {
	type payload struct {
		Value string `json:"value"`
	}
	for _, test := range []struct {
		name  string
		body  string
		valid bool
	}{
		{"valid", `{"value":"ok"}`, true},
		{"trailing whitespace", "{\"value\":\"ok\"}\n ", true},
		{"unknown field", `{"value":"ok","other":1}`, false},
		{"two documents", `{"value":"one"}{"value":"two"}`, false},
		{"empty", ``, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest(http.MethodPost, "/", bytes.NewBufferString(test.body))
			var result payload
			err := decodeJSON(request, &result)
			if (err == nil) != test.valid {
				t.Fatalf("err = %v", err)
			}
		})
	}
}
