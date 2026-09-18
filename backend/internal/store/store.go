package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
)

type Store struct{ Pool *pgxpool.Pool }

func Open(ctx context.Context, databaseURL string) (*Store, error) {
	pool, err := pgxpool.New(ctx, databaseURL)
	if err != nil {
		return nil, fmt.Errorf("configurar banco: %w", err)
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, fmt.Errorf("conectar ao banco: %w", err)
	}
	return &Store{Pool: pool}, nil
}

type Dashboard struct {
	Mode                string     `json:"mode"`
	Open                int        `json:"open"`
	Scheduled           int        `json:"scheduled"`
	Closed              int        `json:"closed"`
	Unknown             int        `json:"unknown"`
	LocationPending     int        `json:"location_pending"`
	JobsPending         int        `json:"jobs_pending"`
	UncertainScheduling int        `json:"uncertain_scheduling"`
	LastSyncSuccess     *time.Time `json:"last_sync_success"`
	LastSyncStatus      *string    `json:"last_sync_status"`
}

func (s *Store) Dashboard(ctx context.Context, mode string) (Dashboard, error) {
	result := Dashboard{Mode: mode}
	err := s.Pool.QueryRow(ctx, `
		SELECT
			count(*) FILTER (WHERE normalized_status = 'open' AND scheduled_at IS NULL),
			count(*) FILTER (WHERE normalized_status = 'scheduled' OR scheduled_at IS NOT NULL),
			count(*) FILTER (WHERE normalized_status = 'closed'),
			count(*) FILTER (WHERE normalized_status = 'unknown'),
			count(*) FILTER (WHERE normalized_status = 'open' AND scheduled_at IS NULL AND l.position IS NULL)
		FROM service_orders o LEFT JOIN order_locations l ON l.order_id = o.id
	`).Scan(&result.Open, &result.Scheduled, &result.Closed, &result.Unknown, &result.LocationPending)
	if err != nil {
		return result, err
	}
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM jobs WHERE status IN ('pending','running')`).Scan(&result.JobsPending); err != nil {
		return result, err
	}
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM scheduling_requests WHERE status = 'uncertain'`).Scan(&result.UncertainScheduling); err != nil {
		return result, err
	}
	var status *string
	var finished *time.Time
	err = s.Pool.QueryRow(ctx, `SELECT status, finished_at FROM sync_runs ORDER BY created_at DESC LIMIT 1`).Scan(&status, &finished)
	if err != nil && !errors.Is(err, pgx.ErrNoRows) {
		return result, err
	}
	result.LastSyncStatus = status
	if finished != nil && status != nil && *status == "completed" {
		result.LastSyncSuccess = finished
	} else {
		_ = s.Pool.QueryRow(ctx, `SELECT finished_at FROM sync_runs WHERE status = 'completed' ORDER BY finished_at DESC LIMIT 1`).Scan(&result.LastSyncSuccess)
	}
	return result, nil
}

type Order struct {
	ID               string     `json:"id"`
	ExternalID       string     `json:"external_id"`
	OperationID      string     `json:"operation_id"`
	Operation        string     `json:"operation"`
	TypeDescription  string     `json:"type_description"`
	SourceStatusCode string     `json:"source_status_code"`
	Status           string     `json:"status"`
	ScheduledAt      *time.Time `json:"scheduled_at"`
	CustomerDisplay  string     `json:"customer_display"`
	DefectSummary    string     `json:"defect_summary"`
	Address          string     `json:"address"`
	Latitude         *float64   `json:"latitude"`
	Longitude        *float64   `json:"longitude"`
	LocationSource   *string    `json:"location_source"`
	ObservedAt       time.Time  `json:"observed_at"`
	ObservedVersion  int64      `json:"observed_version"`
}

func (s *Store) Orders(ctx context.Context, status, operationID string) ([]Order, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT o.id, o.external_id, op.id, op.name, t.description, o.source_status_code,
			o.normalized_status, o.scheduled_at, o.customer_display, coalesce(o.defect_summary,''),
			o.address_original, ST_Y(l.position::geometry), ST_X(l.position::geometry), l.source,
			o.observed_at, o.observed_version
		FROM service_orders o
		JOIN operations op ON op.id = o.operation_id
		LEFT JOIN mk_service_types t ON t.id = o.service_type_id
		LEFT JOIN order_locations l ON l.order_id = o.id
		WHERE ($1 = '' OR o.normalized_status::text = $1)
		  AND ($2 = '' OR o.operation_id::text = $2)
		ORDER BY o.observed_at DESC, o.external_id
		LIMIT 500`, status, operationID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	orders := make([]Order, 0)
	for rows.Next() {
		var order Order
		if err := rows.Scan(&order.ID, &order.ExternalID, &order.OperationID, &order.Operation,
			&order.TypeDescription, &order.SourceStatusCode, &order.Status, &order.ScheduledAt,
			&order.CustomerDisplay, &order.DefectSummary, &order.Address, &order.Latitude,
			&order.Longitude, &order.LocationSource, &order.ObservedAt, &order.ObservedVersion); err != nil {
			return nil, err
		}
		orders = append(orders, order)
	}
	return orders, rows.Err()
}

type Operation struct {
	ID                   string   `json:"id"`
	Slug                 string   `json:"slug"`
	Name                 string   `json:"name"`
	Enabled              bool     `json:"enabled"`
	SyncIntervalSeconds  int      `json:"sync_interval_seconds"`
	Timezone             string   `json:"timezone"`
	GroupingRadiusMeters int      `json:"grouping_radius_meters"`
	ServiceTypeIDs       []string `json:"service_type_ids"`
}

func (s *Store) Operations(ctx context.Context) ([]Operation, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT o.id, o.slug, o.name, o.enabled, o.sync_interval_seconds, o.timezone,
			o.grouping_radius_meters,
			coalesce(array_agg(ost.service_type_id::text) FILTER (WHERE ost.service_type_id IS NOT NULL), '{}')
		FROM operations o LEFT JOIN operation_service_types ost ON ost.operation_id = o.id
		GROUP BY o.id ORDER BY o.name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Operation, 0)
	for rows.Next() {
		var item Operation
		if err := rows.Scan(&item.ID, &item.Slug, &item.Name, &item.Enabled, &item.SyncIntervalSeconds,
			&item.Timezone, &item.GroupingRadiusMeters, &item.ServiceTypeIDs); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type ServiceType struct {
	ID          string    `json:"id"`
	ExternalID  string    `json:"external_id"`
	Description string    `json:"description"`
	Source      string    `json:"source"`
	ObservedAt  time.Time `json:"observed_at"`
}

func (s *Store) ServiceTypes(ctx context.Context) ([]ServiceType, error) {
	rows, err := s.Pool.Query(ctx, `SELECT id, external_id, description, source, observed_at FROM mk_service_types ORDER BY description`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]ServiceType, 0)
	for rows.Next() {
		var item ServiceType
		if err := rows.Scan(&item.ID, &item.ExternalID, &item.Description, &item.Source, &item.ObservedAt); err != nil {
			return nil, err
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

func (s *Store) SetOperationServiceTypes(ctx context.Context, operationID string, typeIDs []string) error {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	var exists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM operations WHERE id = $1)`, operationID).Scan(&exists); err != nil {
		return err
	}
	if !exists {
		return pgx.ErrNoRows
	}
	if _, err := tx.Exec(ctx, `DELETE FROM operation_service_types WHERE operation_id = $1`, operationID); err != nil {
		return err
	}
	for _, typeID := range typeIDs {
		if _, err := tx.Exec(ctx, `INSERT INTO operation_service_types (operation_id, service_type_id) VALUES ($1, $2)`, operationID, typeID); err != nil {
			return fmt.Errorf("associar tipo %s: %w", typeID, err)
		}
	}
	return tx.Commit(ctx)
}

func (s *Store) EnqueueSync(ctx context.Context, operationID, trigger string) (string, bool, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return "", false, err
	}
	defer tx.Rollback(ctx)
	var operationExists bool
	if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM operations WHERE id = $1 AND enabled)`, operationID).Scan(&operationExists); err != nil {
		return "", false, err
	}
	if !operationExists {
		return "", false, pgx.ErrNoRows
	}
	var jobID string
	err = tx.QueryRow(ctx, `
		INSERT INTO jobs (kind, operation_id, payload)
		VALUES ('sync_operation', $1, jsonb_build_object('trigger', $2::text))
		ON CONFLICT (kind, operation_id) WHERE kind = 'sync_operation' AND status IN ('pending','running')
		DO NOTHING RETURNING id`, operationID, trigger).Scan(&jobID)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", false, tx.Commit(ctx)
	}
	if err != nil {
		return "", false, err
	}
	return jobID, true, tx.Commit(ctx)
}

type SyncRun struct {
	ID             string     `json:"id"`
	OperationID    string     `json:"operation_id"`
	Operation      string     `json:"operation"`
	Source         string     `json:"source"`
	Trigger        string     `json:"trigger"`
	Status         string     `json:"status"`
	OrdersSeen     int        `json:"orders_seen"`
	StartedAt      *time.Time `json:"started_at"`
	FinishedAt     *time.Time `json:"finished_at"`
	ErrorCategory  *string    `json:"error_category"`
	SanitizedError *string    `json:"sanitized_error"`
}

func (s *Store) LatestSyncRun(ctx context.Context) (*SyncRun, error) {
	var run SyncRun
	err := s.Pool.QueryRow(ctx, `
		SELECT r.id, r.operation_id, o.name, r.source, r.trigger, r.status, r.orders_seen,
			r.started_at, r.finished_at, r.error_category, r.sanitized_error
		FROM sync_runs r JOIN operations o ON o.id = r.operation_id
		ORDER BY r.created_at DESC LIMIT 1`).Scan(&run.ID, &run.OperationID, &run.Operation,
		&run.Source, &run.Trigger, &run.Status, &run.OrdersSeen, &run.StartedAt, &run.FinishedAt,
		&run.ErrorCategory, &run.SanitizedError)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, nil
	}
	return &run, err
}

type Job struct {
	ID          string
	Kind        string
	OperationID string
	Payload     json.RawMessage
	Attempts    int
}

func (s *Store) ClaimJob(ctx context.Context, workerID string, lease time.Duration) (*Job, error) {
	tx, err := s.Pool.Begin(ctx)
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var job Job
	err = tx.QueryRow(ctx, `
		SELECT id, kind, operation_id, payload, attempts
		FROM jobs
		WHERE ((status = 'pending' AND run_after <= now()) OR (status = 'running' AND lease_until < now()))
		  AND attempts < max_attempts
		ORDER BY run_after, created_at
		FOR UPDATE SKIP LOCKED LIMIT 1`).Scan(&job.ID, &job.Kind, &job.OperationID, &job.Payload, &job.Attempts)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, tx.Commit(ctx)
	}
	if err != nil {
		return nil, err
	}
	_, err = tx.Exec(ctx, `UPDATE jobs SET status='running', attempts=attempts+1, worker_id=$2,
		lease_until=now()+$3::interval, updated_at=now() WHERE id=$1`, job.ID, workerID, lease.String())
	if err != nil {
		return nil, err
	}
	job.Attempts++
	return &job, tx.Commit(ctx)
}

func (s *Store) CompleteJob(ctx context.Context, jobID string) error {
	_, err := s.Pool.Exec(ctx, `UPDATE jobs SET status='completed', lease_until=NULL, updated_at=now() WHERE id=$1`, jobID)
	return err
}

func (s *Store) FailJob(ctx context.Context, jobID, message string, retryAfter time.Duration) error {
	_, err := s.Pool.Exec(ctx, `UPDATE jobs SET
		status=CASE WHEN attempts >= max_attempts THEN 'failed'::job_status ELSE 'pending'::job_status END,
		lease_until=NULL, worker_id=NULL, last_error=$2, run_after=now()+$3::interval, updated_at=now()
		WHERE id=$1`, jobID, message, retryAfter.String())
	return err
}
