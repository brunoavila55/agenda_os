// This file uses the external store_test package (rather than store) so it
// can import testdb, which itself depends on store — an internal test file
// (package store) importing testdb would be a real import cycle.
package store_test

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"agendaos/internal/store"
	"agendaos/internal/testdb"
)

// These tests exercise the job queue against a real PostgreSQL/PostGIS
// instance because the guarantees they check — FOR UPDATE SKIP LOCKED never
// handing the same row to two workers, a lease actually expiring, the
// unique-active-job index rejecting concurrent inserts — cannot be verified
// against a mock. They are skipped automatically when TEST_DATABASE_URL is
// not set; see backend/internal/testdb and scripts/postgres-tests.sh.

func ruralOperationID(t *testing.T, ctx context.Context, s *store.Store) string {
	t.Helper()
	var id string
	if err := s.Pool.QueryRow(ctx, `SELECT id FROM operations WHERE slug = 'rural'`).Scan(&id); err != nil {
		t.Fatalf("obter operação semeada: %v", err)
	}
	return id
}

func insertTestJob(t *testing.T, ctx context.Context, s *store.Store, operationID string, maxAttempts int) string {
	t.Helper()
	var id string
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO jobs (kind, operation_id, payload, max_attempts)
		VALUES ('concurrency_test', $1, '{}'::jsonb, $2)
		RETURNING id`, operationID, maxAttempts).Scan(&id)
	if err != nil {
		t.Fatalf("inserir tarefa de teste: %v", err)
	}
	return id
}

func TestClaimJob_ConcurrentWorkersNeverShareAJob(t *testing.T) {
	s := testdb.New(t)
	ctx := context.Background()
	operationID := ruralOperationID(t, ctx, s)

	const jobCount = 12
	for i := 0; i < jobCount; i++ {
		insertTestJob(t, ctx, s, operationID, 5)
	}

	var mu sync.Mutex
	claimedBy := make(map[string]string, jobCount)

	const workerCount = 6
	var wg sync.WaitGroup
	for w := 0; w < workerCount; w++ {
		workerID := fmt.Sprintf("worker-%d", w)
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				job, err := s.ClaimJob(ctx, workerID, time.Minute)
				if err != nil {
					t.Errorf("ClaimJob(%s): %v", workerID, err)
					return
				}
				if job == nil {
					return
				}
				mu.Lock()
				if existing, ok := claimedBy[job.ID]; ok {
					t.Errorf("tarefa %s reservada por %s e por %s ao mesmo tempo", job.ID, existing, workerID)
				}
				claimedBy[job.ID] = workerID
				mu.Unlock()
				if err := s.CompleteJob(ctx, job.ID); err != nil {
					t.Errorf("CompleteJob(%s): %v", job.ID, err)
				}
			}
		}()
	}
	wg.Wait()

	if len(claimedBy) != jobCount {
		t.Fatalf("tarefas reservadas = %d, want %d", len(claimedBy), jobCount)
	}
}

func TestClaimJob_ExpiredLeaseIsReclaimedAfterSimulatedRestart(t *testing.T) {
	s := testdb.New(t)
	ctx := context.Background()
	operationID := ruralOperationID(t, ctx, s)
	jobID := insertTestJob(t, ctx, s, operationID, 5)

	// A lease de um minuto nunca deveria expirar sozinha durante o teste;
	// a expiração abaixo é forçada via SQL para não depender de tempo real
	// (sob -race ou uma máquina ocupada, uma lease curta pode expirar antes
	// da verificação seguinte e tornar o teste instável).
	first, err := s.ClaimJob(ctx, "worker-a", time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob inicial: %v", err)
	}
	if first == nil || first.ID != jobID {
		t.Fatalf("ClaimJob inicial não retornou a tarefa esperada: %+v", first)
	}
	if first.Attempts != 1 {
		t.Fatalf("attempts = %d, want 1", first.Attempts)
	}

	// worker-b tenta pegar trabalho enquanto worker-a ainda está processando:
	// a lease ativa deve impedir a reserva duplicada.
	stillLeased, err := s.ClaimJob(ctx, "worker-b", time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob concorrente: %v", err)
	}
	if stillLeased != nil {
		t.Fatalf("tarefa com lease ativa foi reservada por outro worker: %+v", stillLeased)
	}

	if _, err := s.Pool.Exec(ctx, `UPDATE jobs SET lease_until = now() - interval '1 second' WHERE id = $1`, jobID); err != nil {
		t.Fatalf("forçar expiração da lease: %v", err)
	}

	// worker-a nunca voltou (processo reiniciado durante o processamento); a
	// lease expirada deve ser recuperável, com a tentativa incrementada.
	reclaimed, err := s.ClaimJob(ctx, "worker-b", time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob após expirar a lease: %v", err)
	}
	if reclaimed == nil || reclaimed.ID != jobID {
		t.Fatalf("lease expirada não foi recuperada: %+v", reclaimed)
	}
	if reclaimed.Attempts != 2 {
		t.Fatalf("attempts após recuperação = %d, want 2", reclaimed.Attempts)
	}

	if err := s.CompleteJob(ctx, jobID); err != nil {
		t.Fatalf("CompleteJob: %v", err)
	}
	after, err := s.ClaimJob(ctx, "worker-c", time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob após concluir: %v", err)
	}
	if after != nil {
		t.Fatalf("tarefa concluída ainda pôde ser reservada: %+v", after)
	}
}

func TestFailJob_ReachesMaxAttemptsAndStopsBeingClaimable(t *testing.T) {
	s := testdb.New(t)
	ctx := context.Background()
	operationID := ruralOperationID(t, ctx, s)
	jobID := insertTestJob(t, ctx, s, operationID, 2)

	for attempt := 1; attempt <= 2; attempt++ {
		job, err := s.ClaimJob(ctx, "worker-a", time.Minute)
		if err != nil {
			t.Fatalf("ClaimJob (tentativa %d): %v", attempt, err)
		}
		if job == nil || job.ID != jobID {
			t.Fatalf("tentativa %d: tarefa não reservada: %+v", attempt, job)
		}
		if job.Attempts != attempt {
			t.Fatalf("attempts = %d, want %d", job.Attempts, attempt)
		}
		if err := s.FailJob(ctx, jobID, "falha simulada", 0); err != nil {
			t.Fatalf("FailJob (tentativa %d): %v", attempt, err)
		}
	}

	var status string
	if err := s.Pool.QueryRow(ctx, `SELECT status FROM jobs WHERE id = $1`, jobID).Scan(&status); err != nil {
		t.Fatalf("consultar status final: %v", err)
	}
	if status != "failed" {
		t.Fatalf("status final = %q, want failed", status)
	}

	stillClaimable, err := s.ClaimJob(ctx, "worker-b", time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob após esgotar tentativas: %v", err)
	}
	if stillClaimable != nil {
		t.Fatalf("tarefa esgotada ainda foi reservada: %+v", stillClaimable)
	}
}

func TestFailJob_DelaysNextClaimUntilRunAfter(t *testing.T) {
	s := testdb.New(t)
	ctx := context.Background()
	operationID := ruralOperationID(t, ctx, s)
	jobID := insertTestJob(t, ctx, s, operationID, 5)

	job, err := s.ClaimJob(ctx, "worker-a", time.Minute)
	if err != nil || job == nil {
		t.Fatalf("ClaimJob inicial: job=%+v err=%v", job, err)
	}
	// Um atraso de uma hora nunca deveria vencer sozinho durante o teste; o
	// vencimento abaixo é forçado via SQL pelo mesmo motivo do teste de lease.
	if err := s.FailJob(ctx, jobID, "falha temporária", time.Hour); err != nil {
		t.Fatalf("FailJob: %v", err)
	}

	tooSoon, err := s.ClaimJob(ctx, "worker-b", time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob antes do atraso: %v", err)
	}
	if tooSoon != nil {
		t.Fatalf("tarefa foi reservada antes do run_after: %+v", tooSoon)
	}

	if _, err := s.Pool.Exec(ctx, `UPDATE jobs SET run_after = now() - interval '1 second' WHERE id = $1`, jobID); err != nil {
		t.Fatalf("forçar vencimento do atraso: %v", err)
	}

	retried, err := s.ClaimJob(ctx, "worker-b", time.Minute)
	if err != nil {
		t.Fatalf("ClaimJob após o atraso: %v", err)
	}
	if retried == nil || retried.ID != jobID {
		t.Fatalf("tarefa não foi reservada após o atraso: %+v", retried)
	}
	if retried.Attempts != 2 {
		t.Fatalf("attempts = %d, want 2", retried.Attempts)
	}
}

func TestEnqueueSync_ConcurrentCallsCreateExactlyOneJob(t *testing.T) {
	s := testdb.New(t)
	ctx := context.Background()
	operationID := ruralOperationID(t, ctx, s)

	const attempts = 20
	var created int64
	var wg sync.WaitGroup
	errs := make(chan error, attempts)
	for i := 0; i < attempts; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, wasCreated, err := s.EnqueueSync(ctx, operationID, "manual")
			if err != nil {
				errs <- err
				return
			}
			if wasCreated {
				atomic.AddInt64(&created, 1)
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatalf("EnqueueSync concorrente: %v", err)
	}
	if created != 1 {
		t.Fatalf("jobs criados = %d, want 1", created)
	}

	var count int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE operation_id = $1 AND kind = 'sync_operation' AND status IN ('pending','running')`, operationID).Scan(&count); err != nil {
		t.Fatalf("contar jobs ativos: %v", err)
	}
	if count != 1 {
		t.Fatalf("jobs ativos no banco = %d, want 1", count)
	}
}

func TestEnqueueDueSync_RespectsPersistedIntervalAndEnabledFlag(t *testing.T) {
	s := testdb.New(t)
	ctx := context.Background()
	operationID := ruralOperationID(t, ctx, s)
	operation := store.Operation{ID: operationID, Enabled: true, SyncIntervalSeconds: 600}

	created, err := s.EnqueueDueSync(ctx, operation, "simulation")
	if err != nil {
		t.Fatalf("EnqueueDueSync sem execuções anteriores: %v", err)
	}
	if !created {
		t.Fatalf("esperava criar tarefa quando não há sync_runs anterior")
	}
	if _, err := s.Pool.Exec(ctx, `UPDATE jobs SET status = 'completed' WHERE operation_id = $1`, operationID); err != nil {
		t.Fatalf("concluir tarefa de sincronização: %v", err)
	}

	if _, err := s.Pool.Exec(ctx, `INSERT INTO sync_runs (operation_id, source, trigger, status, started_at, finished_at)
		VALUES ($1, 'simulation', 'automatic', 'completed', now(), now())`, operationID); err != nil {
		t.Fatalf("registrar execução recente: %v", err)
	}
	createdAgain, err := s.EnqueueDueSync(ctx, operation, "simulation")
	if err != nil {
		t.Fatalf("EnqueueDueSync logo após execução: %v", err)
	}
	if createdAgain {
		t.Fatalf("não deveria criar tarefa antes do intervalo configurado vencer")
	}

	if _, err := s.Pool.Exec(ctx, `UPDATE sync_runs SET created_at = now() - make_interval(secs => $2) WHERE operation_id = $1`,
		operationID, operation.SyncIntervalSeconds+30); err != nil {
		t.Fatalf("envelhecer execução anterior: %v", err)
	}
	createdOnceDue, err := s.EnqueueDueSync(ctx, operation, "simulation")
	if err != nil {
		t.Fatalf("EnqueueDueSync após o intervalo vencer: %v", err)
	}
	if !createdOnceDue {
		t.Fatalf("esperava nova tarefa após o intervalo configurado vencer")
	}

	if _, err := s.Pool.Exec(ctx, `UPDATE jobs SET status = 'completed' WHERE operation_id = $1`, operationID); err != nil {
		t.Fatalf("concluir tarefa: %v", err)
	}
	if _, err := s.Pool.Exec(ctx, `DELETE FROM sync_runs WHERE operation_id = $1`, operationID); err != nil {
		t.Fatalf("limpar execuções: %v", err)
	}
	disabled := operation
	disabled.Enabled = false
	createdWhileDisabled, err := s.EnqueueDueSync(ctx, disabled, "simulation")
	if err != nil {
		t.Fatalf("EnqueueDueSync com operação desabilitada: %v", err)
	}
	if createdWhileDisabled {
		t.Fatalf("operação desabilitada não deveria enfileirar sincronização")
	}
}
