package store

import (
	"context"
	"errors"

	"github.com/jackc/pgx/v5"
)

func (s *Store) OrderByID(ctx context.Context, id, source string) (*Order, error) {
	var order Order
	err := s.Pool.QueryRow(ctx, `
		SELECT o.id,o.external_id,op.id,op.name,t.description,o.source_status_code,
			o.normalized_status,o.scheduled_at,o.customer_display,coalesce(o.defect_summary,''),
			o.address_original,ST_Y(l.position::geometry),ST_X(l.position::geometry),l.source,
			coalesce(l.location_version,0),coalesce(l.address_changed,false) OR l.position IS NULL,
			o.observed_at,o.observed_version
		FROM service_orders o JOIN operations op ON op.id=o.operation_id
		LEFT JOIN mk_service_types t ON t.id=o.service_type_id
		LEFT JOIN order_locations l ON l.order_id=o.id
		WHERE o.id=$1 AND o.source=$2`, id, source).Scan(&order.ID, &order.ExternalID, &order.OperationID,
		&order.Operation, &order.TypeDescription, &order.SourceStatusCode, &order.Status, &order.ScheduledAt,
		&order.CustomerDisplay, &order.DefectSummary, &order.Address, &order.Latitude, &order.Longitude,
		&order.LocationSource, &order.LocationVersion, &order.LocationNeedsReview, &order.ObservedAt, &order.ObservedVersion)
	if err != nil {
		return nil, err
	}
	return &order, nil
}

func (s *Store) SetManualLocation(ctx context.Context, id, source string, latitude, longitude float64, expectedOrderVersion, expectedLocationVersion int64) (*Order, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var orderVersion, locationVersion int64
	err = tx.QueryRow(ctx, `SELECT o.observed_version,coalesce(l.location_version,0)
		FROM service_orders o LEFT JOIN order_locations l ON l.order_id=o.id
		WHERE o.id=$1 AND o.source=$2 FOR UPDATE OF o`, id, source).Scan(&orderVersion, &locationVersion)
	if err != nil {
		return nil, err
	}
	if orderVersion != expectedOrderVersion || locationVersion != expectedLocationVersion {
		return nil, ErrLocationConflict
	}
	var newVersion int64
	err = tx.QueryRow(ctx, `INSERT INTO order_locations (
		order_id,position,source,confidence,reviewed_manually,address_fingerprint,observed_at,location_version,address_changed,updated_at
	)
	SELECT o.id,ST_SetSRID(ST_MakePoint($3,$4),4326)::geography,'manual',1,true,
		encode(digest(o.address_original,'sha256'),'hex'),now(),1,false,now()
	FROM service_orders o WHERE o.id=$1 AND o.source=$2
	ON CONFLICT (order_id) DO UPDATE SET position=EXCLUDED.position,source='manual',confidence=1,
		reviewed_manually=true,address_fingerprint=EXCLUDED.address_fingerprint,observed_at=now(),
		location_version=order_locations.location_version+1,address_changed=false,updated_at=now()
	WHERE order_locations.location_version=$5
	RETURNING location_version`, id, source, longitude, latitude, expectedLocationVersion).Scan(&newVersion)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, ErrLocationConflict
	}
	if err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO events (aggregate_type,aggregate_id,event_type,metadata)
		VALUES ('service_order',$1,'location.corrected',jsonb_build_object('location_version',$2::bigint,'source','manual'))`, id, newVersion); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.OrderByID(ctx, id, source)
}
