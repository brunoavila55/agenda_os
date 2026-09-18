package syncer

import (
	"context"
	"testing"

	"agendaos/internal/store"
	"agendaos/internal/testdb"
)

// RunSimulation stands in for the future MK sync: it must be safe to run
// repeatedly (the worker retries and reruns on its own interval) and a
// partial failure must not leave inserted rows behind. Both properties
// depend on real transaction/constraint behavior, so these run against
// PostgreSQL/PostGIS via testdb and are skipped without TEST_DATABASE_URL.

func TestRunSimulation_RepeatedRunIsIdempotent(t *testing.T) {
	s := testdb.New(t)
	ctx := context.Background()

	var operationID string
	if err := s.Pool.QueryRow(ctx, `SELECT id FROM operations WHERE slug = 'rural'`).Scan(&operationID); err != nil {
		t.Fatalf("obter operação semeada: %v", err)
	}
	job := &store.Job{OperationID: operationID, Payload: []byte(`{"trigger":"automatic"}`)}

	if err := RunSimulation(ctx, s, job); err != nil {
		t.Fatalf("primeira sincronização: %v", err)
	}

	var firstCount int
	var firstVersionSum int64
	if err := s.Pool.QueryRow(ctx, `SELECT count(*), coalesce(sum(observed_version),0) FROM service_orders WHERE operation_id = $1`,
		operationID).Scan(&firstCount, &firstVersionSum); err != nil {
		t.Fatalf("contar ordens após a primeira sincronização: %v", err)
	}
	if firstCount == 0 {
		t.Fatalf("nenhuma ordem simulada foi importada")
	}

	if err := RunSimulation(ctx, s, job); err != nil {
		t.Fatalf("segunda sincronização: %v", err)
	}

	var secondCount int
	var secondVersionSum int64
	if err := s.Pool.QueryRow(ctx, `SELECT count(*), coalesce(sum(observed_version),0) FROM service_orders WHERE operation_id = $1`,
		operationID).Scan(&secondCount, &secondVersionSum); err != nil {
		t.Fatalf("contar ordens após a segunda sincronização: %v", err)
	}
	if secondCount != firstCount {
		t.Fatalf("repetir a sincronização alterou a quantidade de ordens: %d -> %d", firstCount, secondCount)
	}
	if secondVersionSum != firstVersionSum {
		t.Fatalf("repetir a sincronização com os mesmos dados incrementou observed_version: %d -> %d", firstVersionSum, secondVersionSum)
	}

	var completedRuns int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM sync_runs WHERE operation_id = $1 AND status = 'completed'`,
		operationID).Scan(&completedRuns); err != nil {
		t.Fatalf("contar execuções concluídas: %v", err)
	}
	if completedRuns != 2 {
		t.Fatalf("execuções concluídas = %d, want 2", completedRuns)
	}
}

func TestRunSimulation_FailurePreservesPriorStateAndRecordsRun(t *testing.T) {
	s := testdb.New(t)
	ctx := context.Background()

	var operationID string
	if err := s.Pool.QueryRow(ctx, `INSERT INTO operations (slug, name, timezone) VALUES ('sem-tipo', 'Operação sem tipo', 'America/Sao_Paulo') RETURNING id`).
		Scan(&operationID); err != nil {
		t.Fatalf("criar operação sem tipo simulado configurado: %v", err)
	}
	job := &store.Job{OperationID: operationID, Payload: []byte(`{"trigger":"automatic"}`)}

	if err := RunSimulation(ctx, s, job); err == nil {
		t.Fatalf("esperava falha por falta de tipo simulado configurado para a operação")
	}

	var count int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM service_orders WHERE operation_id = $1`, operationID).Scan(&count); err != nil {
		t.Fatalf("contar ordens: %v", err)
	}
	if count != 0 {
		t.Fatalf("falha durante a sincronização deixou %d ordens persistidas", count)
	}

	var status, category string
	if err := s.Pool.QueryRow(ctx, `SELECT status, error_category FROM sync_runs WHERE operation_id = $1`, operationID).
		Scan(&status, &category); err != nil {
		t.Fatalf("consultar execução registrada: %v", err)
	}
	if status != "failed" {
		t.Fatalf("status = %q, want failed", status)
	}
	if category != "simulation" {
		t.Fatalf("error_category = %q, want simulation", category)
	}
}
