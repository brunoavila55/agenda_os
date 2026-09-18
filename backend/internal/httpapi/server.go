package httpapi

import (
	"crypto/subtle"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
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
	mux.Handle("PUT /api/v1/orders/{id}/location", server.auth(http.HandlerFunc(server.setOrderLocation)))
	mux.Handle("GET /api/v1/operations", server.auth(http.HandlerFunc(server.operations)))
	mux.Handle("GET /api/v1/service-types", server.auth(http.HandlerFunc(server.serviceTypes)))
	mux.Handle("GET /api/v1/teams", server.auth(http.HandlerFunc(server.teams)))
	mux.Handle("PUT /api/v1/operations/{id}/service-types", server.auth(http.HandlerFunc(server.setServiceTypes)))
	mux.Handle("POST /api/v1/sync-runs", server.auth(http.HandlerFunc(server.enqueueSync)))
	mux.Handle("GET /api/v1/sync-runs/latest", server.auth(http.HandlerFunc(server.latestSync)))
	mux.Handle("GET /api/v1/planning-proposals", server.auth(http.HandlerFunc(server.proposalForDate)))
	mux.Handle("POST /api/v1/planning-proposals", server.auth(http.HandlerFunc(server.createProposal)))
	mux.Handle("PUT /api/v1/planning-proposals/{id}", server.auth(http.HandlerFunc(server.updateProposal)))
	mux.Handle("POST /api/v1/planning-proposals/{id}/refresh", server.auth(http.HandlerFunc(server.refreshProposal)))
	mux.Handle("POST /api/v1/planning-proposals/{id}/approve", server.auth(http.HandlerFunc(server.approveProposal)))
	mux.Handle("GET /api/v1/planning-proposals/{id}/scheduling-preview", server.auth(http.HandlerFunc(server.schedulingPreview)))
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
	page, err := positiveQueryInt(r, "page", 1, 1000000)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_page", "page deve ser um inteiro positivo.")
		return
	}
	pageSize, err := positiveQueryInt(r, "page_size", 100, 200)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_page_size", "page_size deve ser um inteiro entre 1 e 200.")
		return
	}
	items, total, err := s.store.Orders(r.Context(), status, r.URL.Query().Get("operation_id"), s.mode,
		strings.TrimSpace(r.URL.Query().Get("q")), page, pageSize)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items, "page": page, "page_size": pageSize, "total": total})
}

func positiveQueryInt(r *http.Request, name string, fallback, maximum int) (int, error) {
	value := r.URL.Query().Get(name)
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed < 1 || parsed > maximum {
		return 0, errors.New("inteiro fora do intervalo")
	}
	return parsed, nil
}

func (s *Server) setOrderLocation(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Latitude                *float64 `json:"latitude"`
		Longitude               *float64 `json:"longitude"`
		ExpectedOrderVersion    int64    `json:"expected_order_version"`
		ExpectedLocationVersion int64    `json:"expected_location_version"`
	}
	if err := decodeJSON(r, &input); err != nil || input.Latitude == nil || input.Longitude == nil {
		writeError(w, http.StatusBadRequest, "invalid_request", "Latitude, longitude e versões são obrigatórias.")
		return
	}
	if input.ExpectedOrderVersion < 1 || input.ExpectedLocationVersion < 0 ||
		math.IsNaN(*input.Latitude) || math.IsInf(*input.Latitude, 0) || *input.Latitude < -90 || *input.Latitude > 90 ||
		math.IsNaN(*input.Longitude) || math.IsInf(*input.Longitude, 0) || *input.Longitude < -180 || *input.Longitude > 180 {
		writeError(w, http.StatusUnprocessableEntity, "invalid_location", "Coordenadas ou versões inválidas.")
		return
	}
	order, err := s.store.SetManualLocation(r.Context(), r.PathValue("id"), s.mode, *input.Latitude, *input.Longitude,
		input.ExpectedOrderVersion, input.ExpectedLocationVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Ordem não encontrada.")
		return
	}
	if errors.Is(err, store.ErrLocationConflict) {
		writeError(w, http.StatusConflict, "location_conflict", "A ordem ou localização mudou. Atualize os dados antes de salvar.")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, order)
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
	items, err := s.store.ServiceTypes(r.Context(), s.mode)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"items": items})
}

func (s *Server) teams(w http.ResponseWriter, r *http.Request) {
	items, err := s.store.Teams(r.Context(), s.mode)
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
	if err := s.store.SetOperationServiceTypes(r.Context(), r.PathValue("id"), s.mode, input.ServiceTypeIDs); err != nil {
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
	run, err := s.store.LatestSyncRun(r.Context(), s.mode)
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, run)
}

func parseOperationalDate(value string) (time.Time, error) {
	if len(value) != len("2006-01-02") {
		return time.Time{}, errors.New("data inválida")
	}
	date, err := time.Parse("2006-01-02", value)
	if err != nil || date.Format("2006-01-02") != value {
		return time.Time{}, errors.New("data inválida")
	}
	return date, nil
}

func (s *Server) proposalForDate(w http.ResponseWriter, r *http.Request) {
	operationID := r.URL.Query().Get("operation_id")
	date := r.URL.Query().Get("date")
	if operationID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "operation_id é obrigatório.")
		return
	}
	if _, err := parseOperationalDate(date); err != nil {
		writeError(w, http.StatusBadRequest, "invalid_date", "date deve usar o formato AAAA-MM-DD.")
		return
	}
	proposal, err := s.store.ProposalForDate(r.Context(), operationID, date, s.mode)
	if errors.Is(err, pgx.ErrNoRows) {
		writeJSON(w, http.StatusOK, nil)
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, proposal)
}

func (s *Server) createProposal(w http.ResponseWriter, r *http.Request) {
	var input struct {
		OperationID     string `json:"operation_id"`
		OperationalDate string `json:"operational_date"`
	}
	if err := decodeJSON(r, &input); err != nil || input.OperationID == "" {
		writeError(w, http.StatusBadRequest, "invalid_request", "operation_id e operational_date são obrigatórios.")
		return
	}
	date, err := parseOperationalDate(input.OperationalDate)
	if err != nil {
		writeError(w, http.StatusBadRequest, "invalid_date", "operational_date deve usar o formato AAAA-MM-DD.")
		return
	}
	proposal, created, err := s.store.CreateProposal(r.Context(), input.OperationID, date, s.mode)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Operação ativa não encontrada.")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	status := http.StatusOK
	if created {
		status = http.StatusCreated
	}
	writeJSON(w, status, proposal)
}

type proposalUpdateInput struct {
	Version             int     `json:"version"`
	TeamID              *string `json:"team_id"`
	AgendaResponsibleID *string `json:"agenda_responsible_id"`
	Groups              []struct {
		Name     string   `json:"name"`
		IsFixed  bool     `json:"is_fixed"`
		OrderIDs []string `json:"order_ids"`
	} `json:"groups"`
	PendingOrderIDs []string `json:"pending_order_ids"`
}

func (s *Server) updateProposal(w http.ResponseWriter, r *http.Request) {
	var input proposalUpdateInput
	if err := decodeJSON(r, &input); err != nil || input.Version < 1 {
		writeError(w, http.StatusBadRequest, "invalid_request", "Versão e composição da proposta são obrigatórias.")
		return
	}
	groups := make([]store.ProposalGroupInput, len(input.Groups))
	for index, group := range input.Groups {
		groups[index] = store.ProposalGroupInput{Name: strings.TrimSpace(group.Name), IsFixed: group.IsFixed, OrderIDs: group.OrderIDs}
	}
	proposal, err := s.store.UpdateProposal(r.Context(), r.PathValue("id"), s.mode, input.Version,
		input.TeamID, input.AgendaResponsibleID, groups, input.PendingOrderIDs)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Proposta não encontrada.")
		return
	}
	if errors.Is(err, store.ErrProposalConflict) {
		writeError(w, http.StatusConflict, "proposal_conflict", "A proposta foi alterada ou não está mais em edição. Atualize os dados.")
		return
	}
	if errors.Is(err, store.ErrInvalidProposal) {
		writeError(w, http.StatusUnprocessableEntity, "invalid_proposal", err.Error())
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, proposal)
}

func (s *Server) refreshProposal(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Version int `json:"version"`
	}
	if err := decodeJSON(r, &input); err != nil || input.Version < 1 {
		writeError(w, http.StatusBadRequest, "invalid_request", "version é obrigatória.")
		return
	}
	proposal, err := s.store.RefreshProposal(r.Context(), r.PathValue("id"), s.mode, input.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Proposta não encontrada.")
		return
	}
	if errors.Is(err, store.ErrProposalConflict) {
		writeError(w, http.StatusConflict, "proposal_conflict", "A proposta mudou ou não pode ser recalculada. Atualize os dados.")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, proposal)
}

func (s *Server) approveProposal(w http.ResponseWriter, r *http.Request) {
	var input struct {
		Version int `json:"version"`
	}
	if err := decodeJSON(r, &input); err != nil || input.Version < 1 {
		writeError(w, http.StatusBadRequest, "invalid_request", "version é obrigatória.")
		return
	}
	proposal, err := s.store.ApproveProposal(r.Context(), r.PathValue("id"), s.mode, input.Version)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Proposta não encontrada.")
		return
	}
	if errors.Is(err, store.ErrProposalConflict) {
		writeError(w, http.StatusConflict, "proposal_conflict", "A proposta ou uma de suas ordens mudou. Revise antes de aprovar.")
		return
	}
	if errors.Is(err, store.ErrInvalidProposal) {
		writeError(w, http.StatusUnprocessableEntity, "invalid_proposal", err.Error())
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, proposal)
}

func (s *Server) schedulingPreview(w http.ResponseWriter, r *http.Request) {
	preview, err := s.store.SchedulingPreview(r.Context(), r.PathValue("id"), s.mode)
	if errors.Is(err, pgx.ErrNoRows) {
		writeError(w, http.StatusNotFound, "not_found", "Proposta não encontrada.")
		return
	}
	if err != nil {
		s.internalError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, preview)
}

func (s *Server) internalError(w http.ResponseWriter, err error) {
	s.logger.Error("falha interna", "error", err)
	writeError(w, http.StatusInternalServerError, "internal_error", "Não foi possível concluir a operação.")
}

func decodeJSON(r *http.Request, target any) error {
	decoder := json.NewDecoder(io.LimitReader(r.Body, 1<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return err
	}
	if err := decoder.Decode(&struct{}{}); !errors.Is(err, io.EOF) {
		return errors.New("corpo deve conter exatamente um documento JSON")
	}
	return nil
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
