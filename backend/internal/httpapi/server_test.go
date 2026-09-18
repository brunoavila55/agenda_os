package httpapi

import (
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"
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
