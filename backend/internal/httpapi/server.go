package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"agendaos/internal/store"
	"github.com/jackc/pgx/v5"
)

type Server struct {
	store    *store.Store
	mode     string
	apiToken string
	logger   *slog.Logger
}

func New(dataStore *store.Store, mode, apiToken string, logger *slog.Logger) http.Handler {
	server := &Server{store: dataStore, mode: mode, apiToken: apiToken, logger: logger}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", server.live)
	mux.HandleFunc("GET /health/ready", server.ready)
	mux.Handle("GET /api/v1/dashboard", server.auth(http.HandlerFunc(server.dashboard)))
	mux.Handle("GET /api/v1/orders", server.auth(http.HandlerFunc(server.orders)))
	mux.Handle("GET /api/v1/operations", server.auth(http.HandlerFunc(server.operations)))
	mux.Handle("GET /api/v1/service-types", server.auth(http.HandlerFunc(server.serviceTypes)))
	mux.Handle("PUT /api/v1/operations/{id}/service-types", server.auth(http.HandlerFunc(server.setServiceTypes)))
	mux.Handle("POST /api/v1/sync-runs", server.auth(http.HandlerFunc(server.enqueueSync)))
	mux.Handle("GET /api/v1/sync-runs/latest", server.auth(http.HandlerFunc(server.latestSync)))
	return server.logging(mux)
}

func (s *Server) auth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		const prefix = "Bearer "
		header := r.Header.Get("Authorization")
		if !strings.HasPrefix(header, prefix) || subtle.ConstantTimeCompare([]byte(strings.TrimPrefix(header, prefix)), []byte(s.apiToken)) != 1 {
			writeError(w, http.StatusUnauthorized, "unauthorized", "Autenticação obrigatória.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func (s *Server) logging(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		started := time.Now()
		next.ServeHTTP(w, r)
		s.logger.Info("requisição HTTP", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(started).Milliseconds())
	})
}

func (s *Server) live(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "alive"})
}

func (s *Server) ready(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := contextWithTimeout(r, 2*time.Second)
	defer cancel()
	if err := s.store.Pool.Ping(ctx); err != nil {
		writeError(w, http.StatusServiceUnavailable, "database_unavailable", "Banco local indisponível.")
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "ready", "mode": s.mode})
}

func (s *Server) dashboard(w http.ResponseWriter, r *http.Request) {
	data, err := s.store.Dashboard(r.Context(), s.mode)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, data)
}

func (s *Server) orders(w http.ResponseWriter, r *http.Request) {
	status := r.URL.Query().Get("status")
	allowed := map[string]bool{"": true, "open": true, "scheduled": true, "closed": true, "cancelled": true, "unknown": true}
	if !allowed[status] {
		writeError(w, http.StatusBadRequest, "invalid_status", "Filtro de situação inválido.")
		return
	}
	items, err := s.store.Orders(r.Context(), status, r.URL.Query().Get("operation_id"))
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) operations(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.Operations(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) serviceTypes(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.ServiceTypes(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) setServiceTypes(w http.ResponseWriter, r *http.Request) {
	var input struct {
		ServiceTypeIDs []string `json:"service_type_ids"`
	}
	if err := decodeJSON(r, &input); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_json", "Corpo JSON inválido.")
		return
	}
	if err := s.store.SetOperationServiceTypes(r.Context(), r.PathValue("id"), input.ServiceTypeIDs); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "Operação não encontrada.")
			return
		}
		s.internalError(w, err)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) enqueueSync(w http.ResponseWriter, r *http.Request) {
	var input struct {
		OperationID string `json:"operation_id"`
	}
	if err := decodeJSON(r, &input); err != nil || input.OperationID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "operation_id é obrigatório.")
		return
	}
	jobID, created, err := s.store.EnqueueSync(r.Context(), input.OperationID, "manual")
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			writeError(w, http.StatusNotFound, "not_found", "Operação ativa não encontrada.")
			return
		}
		s.internalError(w, err)
		return
	}
	if !created {
		writeJSON(w, http.StatusAccepted, map[string]any{"queued": false, "message": "Já existe uma sincronização pendente ou em execução."})
		return
	}
	writeJSON(w, http.StatusAccepted, map[string]any{"queued": true, "job_id": jobID})
}

func (s *Server) latestSync(w http.ResponseWriter, r *http.Request) {
	run, err := s.store.LatestSyncRun(r.Context())
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func (s *Server) internalError(w http.ResponseWriter, err error) {
	s.logger.Error("falha interna", "error", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "Não foi possível concluir a operação.")
}

func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	return decoder.Decode(target)
}

func writeError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]any{"error": map[string]string{"code": code, "message": message}})
}

func writeJSON(w http.ResponseWriter, status int, value any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(value)
}
