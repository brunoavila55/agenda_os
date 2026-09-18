package syncer

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"time"

	"agendaos/internal/store"
)

type fixtureOrder struct {
	ExternalID string
	StatusCode string
	Status     string
	Scheduled  bool
	Customer   string
	Defect     string
	Address    string
	Latitude   *float64
	Longitude  *float64
}

func number(value float64) *float64 { return &value }

var fixtures = []fixtureOrder{
	{"SIM-OS-1001", "AB", "open", false, "Cliente rural 01", "Sem conexão", "Estrada Rural Norte, km 12 — área simulada", number(-23.5038), number(-46.8467)},
	{"SIM-OS-1002", "AB", "open", false, "Cliente rural 02", "Sinal instável", "Estrada Rural Norte, km 15 — área simulada", number(-23.5161), number(-46.8293)},
	{"SIM-OS-1003", "AB", "open", false, "Cliente rural 03", "Equipamento sem energia", "Bairro dos Pinhais — área simulada", number(-23.5642), number(-46.8031)},
	{"SIM-OS-1004", "AB", "open", false, "Cliente rural 04", "Rompimento aparente", "Sítio Boa Vista — área simulada", number(-23.5730), number(-46.7900)},
	{"SIM-OS-1005", "AB", "open", false, "Cliente rural 05", "Visita técnica", "Endereço rural incompleto — área simulada", nil, nil},
	{"SIM-OS-0998", "AG", "scheduled", true, "Cliente rural 06", "Troca de equipamento", "Rodovia Municipal, km 4 — área simulada", number(-23.5350), number(-46.8200)},
	{"SIM-OS-0980", "FE", "closed", false, "Cliente rural 07", "Atendimento concluído", "Bairro Central — área simulada", number(-23.5400), number(-46.8100)},
}

type payload struct {
	Trigger string `json:"trigger"`
}

func RunSimulation(ctx context.Context, dataStore *store.Store, job *store.Job) error {
	var input payload
	if err := json.Unmarshal(job.Payload, &input); err != nil {
		return fmt.Errorf("payload inválido: %w", err)
	}
	if input.Trigger == "" {
		input.Trigger = "automatic"
	}

	tx, err := dataStore.Pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)

	var runID string
	err = tx.QueryRow(ctx, `INSERT INTO sync_runs (operation_id, source, trigger, status, started_at)
		VALUES ($1, 'simulation', $2, 'running', now()) RETURNING id`, job.OperationID, input.Trigger).Scan(&runID)
	if err != nil {
		return err
	}

	for _, item := range fixtures {
		var typeID string
		err := tx.QueryRow(ctx, `SELECT ost.service_type_id FROM operation_service_types ost
			JOIN mk_service_types t ON t.id=ost.service_type_id
			WHERE ost.operation_id=$1 AND t.source='simulation' ORDER BY t.external_id LIMIT 1`, job.OperationID).Scan(&typeID)
		if err != nil {
			return fmt.Errorf("operação sem tipo simulado configurado: %w", err)
		}

		var orderID string
		var scheduledAt any
		if item.Scheduled {
			scheduledAt = time.Now().UTC().Truncate(24 * time.Hour).Add(36 * time.Hour)
		}
		err = tx.QueryRow(ctx, `
			INSERT INTO service_orders (
				source, external_id, operation_id, service_type_id, source_status_code,
				normalized_status, scheduled_at, customer_display, defect_summary,
				address_original, observed_at
			) VALUES ('simulation',$1,$2,$3,$4,$5::normalized_order_status,$6,$7,$8,$9,now())
			ON CONFLICT (source, external_id) DO UPDATE SET
				operation_id=EXCLUDED.operation_id, service_type_id=EXCLUDED.service_type_id,
				source_status_code=EXCLUDED.source_status_code, normalized_status=EXCLUDED.normalized_status,
				scheduled_at=EXCLUDED.scheduled_at, customer_display=EXCLUDED.customer_display,
				defect_summary=EXCLUDED.defect_summary, address_original=EXCLUDED.address_original,
				observed_version=service_orders.observed_version + CASE WHEN
					(service_orders.source_status_code, service_orders.normalized_status, service_orders.scheduled_at,
					 service_orders.address_original, service_orders.defect_summary)
					IS DISTINCT FROM
					(EXCLUDED.source_status_code, EXCLUDED.normalized_status, EXCLUDED.scheduled_at,
					 EXCLUDED.address_original, EXCLUDED.defect_summary) THEN 1 ELSE 0 END,
				observed_at=now(), updated_at=now()
			RETURNING id`, item.ExternalID, job.OperationID, typeID, item.StatusCode, item.Status,
			scheduledAt, item.Customer, item.Defect, item.Address).Scan(&orderID)
		if err != nil {
			return fmt.Errorf("atualizar ordem %s: %w", item.ExternalID, err)
		}

		fingerprintBytes := sha256.Sum256([]byte(item.Address))
		fingerprint := hex.EncodeToString(fingerprintBytes[:])
		if item.Latitude == nil || item.Longitude == nil {
			_, err = tx.Exec(ctx, `INSERT INTO order_locations (order_id, position, source, confidence, address_fingerprint, observed_at)
				VALUES ($1,NULL,'unresolved',NULL,$2,now()) ON CONFLICT (order_id) DO UPDATE SET
				position=NULL, source='unresolved', confidence=NULL, address_fingerprint=$2, observed_at=now()
				WHERE NOT order_locations.reviewed_manually`, orderID, fingerprint)
		} else {
			_, err = tx.Exec(ctx, `INSERT INTO order_locations (order_id, position, source, confidence, address_fingerprint, observed_at)
				VALUES ($1,ST_SetSRID(ST_MakePoint($2,$3),4326)::geography,'fixture',1,$4,now())
				ON CONFLICT (order_id) DO UPDATE SET position=EXCLUDED.position, source='fixture', confidence=1,
				address_fingerprint=$4, observed_at=now() WHERE NOT order_locations.reviewed_manually`,
				orderID, *item.Longitude, *item.Latitude, fingerprint)
		}
		if err != nil {
			return fmt.Errorf("atualizar localização %s: %w", item.ExternalID, err)
		}
	}

	_, err = tx.Exec(ctx, `UPDATE sync_runs SET status='completed', orders_seen=$2, finished_at=now() WHERE id=$1`, runID, len(fixtures))
	if err != nil {
		return err
	}
	_, err = tx.Exec(ctx, `INSERT INTO events (aggregate_type, aggregate_id, event_type, metadata)
		VALUES ('sync_run',$1,'sync.completed',jsonb_build_object('orders_seen',$2::integer,'source','simulation'))`, runID, len(fixtures))
	if err != nil {
		return err
	}
	return tx.Commit(ctx)
}
