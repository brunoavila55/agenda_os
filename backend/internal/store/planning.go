package store

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/jackc/pgx/v5"
)

var (
	ErrProposalConflict = errors.New("proposal conflict")
	ErrInvalidProposal  = errors.New("invalid proposal")
	ErrLocationConflict = errors.New("location conflict")
)

type TeamMember struct {
	ID         string `json:"id"`
	ExternalID string `json:"external_id"`
	Name       string `json:"name"`
}

type Team struct {
	ID         string       `json:"id"`
	ExternalID string       `json:"external_id"`
	Name       string       `json:"name"`
	Source     string       `json:"source"`
	Members    []TeamMember `json:"members"`
}

func (s *Store) Teams(ctx context.Context, source string) ([]Team, error) {
	rows, err := s.Pool.Query(ctx, `
		SELECT t.id, t.external_id, t.name, t.source,
			coalesce(jsonb_agg(jsonb_build_object('id', tech.id, 'external_id', tech.external_id, 'name', tech.name)
				ORDER BY tech.name) FILTER (WHERE tech.id IS NOT NULL), '[]'::jsonb)
		FROM teams t
		LEFT JOIN team_members tm ON tm.team_id = t.id
		LEFT JOIN technicians tech ON tech.id = tm.technician_id
		WHERE t.source = $1
		GROUP BY t.id
		ORDER BY t.name`, source)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	items := make([]Team, 0)
	for rows.Next() {
		var item Team
		var membersJSON []byte
		if err := rows.Scan(&item.ID, &item.ExternalID, &item.Name, &item.Source, &membersJSON); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(membersJSON, &item.Members); err != nil {
			return nil, fmt.Errorf("decodificar integrantes: %w", err)
		}
		items = append(items, item)
	}
	return items, rows.Err()
}

type ProposalOrder struct {
	ID              string   `json:"id"`
	ExternalID      string   `json:"external_id"`
	CustomerDisplay string   `json:"customer_display"`
	DefectSummary   string   `json:"defect_summary"`
	Address         string   `json:"address"`
	Latitude        *float64 `json:"latitude"`
	Longitude       *float64 `json:"longitude"`
	ObservedVersion int64    `json:"observed_version"`
}

type ProposalGroup struct {
	ID       string          `json:"id"`
	Name     string          `json:"name"`
	Position int             `json:"position"`
	IsFixed  bool            `json:"is_fixed"`
	Orders   []ProposalOrder `json:"orders"`
}

type Proposal struct {
	ID                  string          `json:"id"`
	Source              string          `json:"source"`
	OperationID         string          `json:"operation_id"`
	Operation           string          `json:"operation"`
	OperationalDate     string          `json:"operational_date"`
	Status              string          `json:"status"`
	Version             int             `json:"version"`
	TeamID              *string         `json:"team_id"`
	AgendaResponsibleID *string         `json:"agenda_responsible_id"`
	Generator           string          `json:"generator"`
	ApprovedAt          *time.Time      `json:"approved_at"`
	CreatedAt           time.Time       `json:"created_at"`
	UpdatedAt           time.Time       `json:"updated_at"`
	Groups              []ProposalGroup `json:"groups"`
	Pending             []ProposalOrder `json:"pending"`
}

type proposalCandidate struct {
	ID       string
	HasPoint bool
}

// connectedComponents turns the proximity pairs calculated by PostGIS into
// stable connected groups. IDs without a position are deliberately omitted.
func connectedComponents(ids []string, edges [][2]string) [][]string {
	parent := make(map[string]string, len(ids))
	for _, id := range ids {
		parent[id] = id
	}
	var find func(string) string
	find = func(id string) string {
		if parent[id] != id {
			parent[id] = find(parent[id])
		}
		return parent[id]
	}
	for _, edge := range edges {
		a, okA := parent[edge[0]]
		b, okB := parent[edge[1]]
		if !okA || !okB {
			continue
		}
		ra, rb := find(a), find(b)
		if ra != rb {
			if ra < rb {
				parent[rb] = ra
			} else {
				parent[ra] = rb
			}
		}
	}
	groups := make(map[string][]string)
	for _, id := range ids {
		root := find(id)
		groups[root] = append(groups[root], id)
	}
	result := make([][]string, 0, len(groups))
	for _, group := range groups {
		sort.Strings(group)
		result = append(result, group)
	}
	sort.Slice(result, func(i, j int) bool { return result[i][0] < result[j][0] })
	return result
}

func (s *Store) CreateProposal(ctx context.Context, operationID string, date time.Time, source string) (*Proposal, bool, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, false, err
	}
	defer tx.Rollback(ctx)
	dateText := date.Format("2006-01-02")
	if _, err := tx.Exec(ctx, `SELECT pg_advisory_xact_lock(hashtextextended($1, 0))`, operationID+":"+dateText); err != nil {
		return nil, false, err
	}

	var existingID string
	err = tx.QueryRow(ctx, `SELECT id FROM planning_proposals WHERE source=$1 AND operation_id=$2 AND operational_date=$3
		AND status IN ('draft','approved','conflicted')`, source, operationID, dateText).Scan(&existingID)
	if err == nil {
		if err := tx.Commit(ctx); err != nil {
			return nil, false, err
		}
		proposal, err := s.Proposal(ctx, existingID)
		return proposal, false, err
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return nil, false, err
	}

	var radius int
	var proposalID string
	err = tx.QueryRow(ctx, `
		INSERT INTO planning_proposals (source, operation_id, operational_date, generator)
		SELECT $3, id, $2, 'deterministic' FROM operations WHERE id=$1 AND enabled
		RETURNING id, (SELECT grouping_radius_meters FROM operations WHERE id=$1)`, operationID, dateText, source).
		Scan(&proposalID, &radius)
	if errors.Is(err, pgx.ErrNoRows) {
		return nil, false, pgx.ErrNoRows
	}
	if err != nil {
		return nil, false, err
	}

	rows, err := tx.Query(ctx, `
		SELECT o.id, l.position IS NOT NULL AND NOT l.address_changed
		FROM service_orders o
		LEFT JOIN order_locations l ON l.order_id=o.id
		WHERE o.operation_id=$1 AND o.source=$2 AND o.normalized_status='open' AND o.scheduled_at IS NULL
		  AND NOT EXISTS (
			SELECT 1 FROM proposal_items pi JOIN planning_proposals p ON p.id=pi.proposal_id
			WHERE pi.order_id=o.id AND p.status IN ('draft','approved','conflicted') AND p.id<>$3
		  )
		ORDER BY o.external_id`, operationID, source, proposalID)
	if err != nil {
		return nil, false, err
	}
	candidates := make([]proposalCandidate, 0)
	positioned := make([]string, 0)
	for rows.Next() {
		var candidate proposalCandidate
		if err := rows.Scan(&candidate.ID, &candidate.HasPoint); err != nil {
			rows.Close()
			return nil, false, err
		}
		candidates = append(candidates, candidate)
		if candidate.HasPoint {
			positioned = append(positioned, candidate.ID)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, false, err
	}

	edgeRows, err := tx.Query(ctx, `
		SELECT a.order_id, b.order_id
		FROM order_locations a JOIN order_locations b ON a.order_id < b.order_id
		WHERE a.order_id = ANY($1::uuid[]) AND b.order_id = ANY($1::uuid[])
		  AND a.position IS NOT NULL AND b.position IS NOT NULL
		  AND ST_DWithin(a.position, b.position, $2)`, positioned, radius)
	if err != nil {
		return nil, false, err
	}
	edges := make([][2]string, 0)
	for edgeRows.Next() {
		var edge [2]string
		if err := edgeRows.Scan(&edge[0], &edge[1]); err != nil {
			edgeRows.Close()
			return nil, false, err
		}
		edges = append(edges, edge)
	}
	edgeRows.Close()
	if err := edgeRows.Err(); err != nil {
		return nil, false, err
	}

	for index, orderIDs := range connectedComponents(positioned, edges) {
		var groupID string
		if err := tx.QueryRow(ctx, `INSERT INTO proposal_groups (proposal_id,name,position)
			VALUES ($1,$2,$3) RETURNING id`, proposalID, fmt.Sprintf("Grupo %d", index+1), index+1).Scan(&groupID); err != nil {
			return nil, false, err
		}
		for _, orderID := range orderIDs {
			if _, err := tx.Exec(ctx, `INSERT INTO proposal_items (proposal_id,order_id,group_id,observed_order_version,observed_location_version)
				SELECT $1,o.id,$2,o.observed_version,l.location_version FROM service_orders o LEFT JOIN order_locations l ON l.order_id=o.id WHERE o.id=$3`, proposalID, groupID, orderID); err != nil {
				return nil, false, err
			}
		}
	}
	for _, candidate := range candidates {
		if candidate.HasPoint {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO proposal_items (proposal_id,order_id,is_pending,observed_order_version,observed_location_version)
			SELECT $1,o.id,true,o.observed_version,l.location_version FROM service_orders o LEFT JOIN order_locations l ON l.order_id=o.id WHERE o.id=$2`, proposalID, candidate.ID); err != nil {
			return nil, false, err
		}
	}
	if _, err := tx.Exec(ctx, `INSERT INTO events (aggregate_type,aggregate_id,event_type,metadata)
		VALUES ('planning_proposal',$1,'proposal.created',jsonb_build_object('generator','deterministic','orders',$2::integer))`, proposalID, len(candidates)); err != nil {
		return nil, false, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, false, err
	}
	proposal, err := s.Proposal(ctx, proposalID)
	return proposal, true, err
}

func (s *Store) ProposalForDate(ctx context.Context, operationID, date, source string) (*Proposal, error) {
	var id string
	err := s.Pool.QueryRow(ctx, `SELECT id FROM planning_proposals WHERE operation_id=$1 AND operational_date=$2 AND source=$3
		AND status IN ('draft','approved','conflicted')`, operationID, date, source).Scan(&id)
	if err != nil {
		return nil, err
	}
	return s.Proposal(ctx, id)
}

func (s *Store) Proposal(ctx context.Context, id string) (*Proposal, error) {
	var result Proposal
	err := s.Pool.QueryRow(ctx, `SELECT p.id,p.source,p.operation_id,o.name,p.operational_date::text,p.status,p.version,
		p.team_id,p.agenda_responsible_id,p.generator,p.approved_at,p.created_at,p.updated_at
		FROM planning_proposals p JOIN operations o ON o.id=p.operation_id WHERE p.id=$1`, id).
		Scan(&result.ID, &result.Source, &result.OperationID, &result.Operation, &result.OperationalDate, &result.Status,
			&result.Version, &result.TeamID, &result.AgendaResponsibleID, &result.Generator,
			&result.ApprovedAt, &result.CreatedAt, &result.UpdatedAt)
	if err != nil {
		return nil, err
	}
	result.Groups = make([]ProposalGroup, 0)
	result.Pending = make([]ProposalOrder, 0)
	groupRows, err := s.Pool.Query(ctx, `SELECT id,name,position,is_fixed FROM proposal_groups WHERE proposal_id=$1 ORDER BY position`, id)
	if err != nil {
		return nil, err
	}
	for groupRows.Next() {
		var group ProposalGroup
		group.Orders = make([]ProposalOrder, 0)
		if err := groupRows.Scan(&group.ID, &group.Name, &group.Position, &group.IsFixed); err != nil {
			groupRows.Close()
			return nil, err
		}
		result.Groups = append(result.Groups, group)
	}
	groupRows.Close()
	if err := groupRows.Err(); err != nil {
		return nil, err
	}
	itemRows, err := s.Pool.Query(ctx, `SELECT pi.group_id,pi.is_pending,o.id,o.external_id,o.customer_display,
		coalesce(o.defect_summary,''),o.address_original,ST_Y(l.position::geometry),ST_X(l.position::geometry),pi.observed_order_version
		FROM proposal_items pi JOIN service_orders o ON o.id=pi.order_id
		LEFT JOIN order_locations l ON l.order_id=o.id WHERE pi.proposal_id=$1 ORDER BY o.external_id`, id)
	if err != nil {
		return nil, err
	}
	defer itemRows.Close()
	for itemRows.Next() {
		var groupID *string
		var pending bool
		var order ProposalOrder
		if err := itemRows.Scan(&groupID, &pending, &order.ID, &order.ExternalID, &order.CustomerDisplay,
			&order.DefectSummary, &order.Address, &order.Latitude, &order.Longitude, &order.ObservedVersion); err != nil {
			return nil, err
		}
		if pending {
			result.Pending = append(result.Pending, order)
			continue
		}
		if groupID != nil {
			for index := range result.Groups {
				if result.Groups[index].ID == *groupID {
					result.Groups[index].Orders = append(result.Groups[index].Orders, order)
					break
				}
			}
		}
	}
	return &result, itemRows.Err()
}

type ProposalGroupInput struct {
	Name     string
	IsFixed  bool
	OrderIDs []string
}

func (s *Store) UpdateProposal(ctx context.Context, id, source string, expectedVersion int, teamID, responsibleID *string, groups []ProposalGroupInput, pendingIDs []string) (*Proposal, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var status string
	var currentVersion int
	if err := tx.QueryRow(ctx, `SELECT status,version FROM planning_proposals WHERE id=$1 AND source=$2 FOR UPDATE`, id, source).Scan(&status, &currentVersion); err != nil {
		return nil, err
	}
	if status != "draft" || currentVersion != expectedVersion {
		return nil, ErrProposalConflict
	}
	if teamID != nil {
		var valid bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM teams WHERE id=$1 AND source=$2)`, *teamID, source).Scan(&valid); err != nil {
			return nil, err
		}
		if !valid {
			return nil, fmt.Errorf("%w: equipe não pertence à origem ativa", ErrInvalidProposal)
		}
	}
	if responsibleID != nil {
		var valid bool
		if err := tx.QueryRow(ctx, `SELECT EXISTS(SELECT 1 FROM technicians WHERE id=$1 AND source=$2)`, *responsibleID, source).Scan(&valid); err != nil {
			return nil, err
		}
		if !valid {
			return nil, fmt.Errorf("%w: responsável não pertence à origem ativa", ErrInvalidProposal)
		}
	}
	seen := make(map[string]bool)
	for _, group := range groups {
		if group.Name == "" || len(group.OrderIDs) == 0 {
			return nil, fmt.Errorf("%w: grupos precisam de nome e ao menos uma ordem", ErrInvalidProposal)
		}
		for _, orderID := range group.OrderIDs {
			if seen[orderID] {
				return nil, fmt.Errorf("%w: ordem duplicada", ErrInvalidProposal)
			}
			seen[orderID] = true
		}
	}
	for _, orderID := range pendingIDs {
		if seen[orderID] {
			return nil, fmt.Errorf("%w: ordem duplicada", ErrInvalidProposal)
		}
		seen[orderID] = true
	}
	var existingCount int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM proposal_items WHERE proposal_id=$1`, id).Scan(&existingCount); err != nil {
		return nil, err
	}
	if existingCount != len(seen) {
		return nil, fmt.Errorf("%w: todas as ordens devem permanecer em um grupo ou pendência", ErrInvalidProposal)
	}
	var matchedCount int
	ids := make([]string, 0, len(seen))
	for orderID := range seen {
		ids = append(ids, orderID)
	}
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM proposal_items WHERE proposal_id=$1 AND order_id=ANY($2::uuid[])`, id, ids).Scan(&matchedCount); err != nil {
		return nil, err
	}
	if matchedCount != existingCount {
		return nil, fmt.Errorf("%w: a proposta contém ordens desconhecidas", ErrInvalidProposal)
	}
	// First move every item to the valid pending state. The check constraint is
	// immediate, so deleting a group while its item is still marked grouped
	// would temporarily create an invalid row.
	if _, err := tx.Exec(ctx, `UPDATE proposal_items SET group_id=NULL,is_pending=true WHERE proposal_id=$1`, id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM proposal_groups WHERE proposal_id=$1`, id); err != nil {
		return nil, err
	}
	for index, group := range groups {
		var groupID string
		if err := tx.QueryRow(ctx, `INSERT INTO proposal_groups (proposal_id,name,position,is_fixed) VALUES ($1,$2,$3,$4) RETURNING id`, id, group.Name, index+1, group.IsFixed).Scan(&groupID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE proposal_items SET group_id=$3,is_pending=false WHERE proposal_id=$1 AND order_id=ANY($2::uuid[])`, id, group.OrderIDs, groupID); err != nil {
			return nil, err
		}
	}
	if len(pendingIDs) > 0 {
		if _, err := tx.Exec(ctx, `UPDATE proposal_items SET group_id=NULL,is_pending=true WHERE proposal_id=$1 AND order_id=ANY($2::uuid[])`, id, pendingIDs); err != nil {
			return nil, err
		}
	}
	command, err := tx.Exec(ctx, `UPDATE planning_proposals SET team_id=$2,agenda_responsible_id=$3,version=version+1,updated_at=now()
		WHERE id=$1 AND version=$4`, id, teamID, responsibleID, expectedVersion)
	if err != nil {
		return nil, err
	}
	if command.RowsAffected() != 1 {
		return nil, ErrProposalConflict
	}
	if _, err := tx.Exec(ctx, `INSERT INTO events (aggregate_type,aggregate_id,event_type,metadata)
		VALUES ('planning_proposal',$1,'proposal.updated',jsonb_build_object('version',$2::integer))`, id, expectedVersion+1); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.Proposal(ctx, id)
}

// RefreshProposal recalculates every non-fixed group, incorporates newly
// eligible orders and removes orders that are no longer eligible. Fixed groups
// retain their eligible members and names.
func (s *Store) RefreshProposal(ctx context.Context, id, source string, expectedVersion int) (*Proposal, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var status, operationID string
	var version, radius int
	if err := tx.QueryRow(ctx, `SELECT p.status,p.version,p.operation_id,o.grouping_radius_meters
		FROM planning_proposals p JOIN operations o ON o.id=p.operation_id
		WHERE p.id=$1 AND p.source=$2 FOR UPDATE`, id, source).Scan(&status, &version, &operationID, &radius); err != nil {
		return nil, err
	}
	if (status != "draft" && status != "conflicted") || version != expectedVersion {
		return nil, ErrProposalConflict
	}

	// Remove ineligible orders even from fixed groups. A fixed group protects a
	// planning choice, but cannot override observed ERP eligibility.
	if _, err := tx.Exec(ctx, `DELETE FROM proposal_items pi USING proposal_groups g, service_orders o
		WHERE pi.proposal_id=$1 AND pi.group_id=g.id AND g.is_fixed AND pi.order_id=o.id
		  AND (o.source<>$2 OR o.normalized_status<>'open' OR o.scheduled_at IS NOT NULL)`, id, source); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE proposal_items pi SET observed_order_version=o.observed_version,
		observed_location_version=l.location_version
		FROM proposal_groups g, service_orders o LEFT JOIN order_locations l ON l.order_id=o.id
		WHERE pi.proposal_id=$1 AND pi.group_id=g.id AND g.is_fixed AND pi.order_id=o.id`, id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM proposal_items pi WHERE pi.proposal_id=$1 AND (
		pi.group_id IS NULL OR EXISTS (SELECT 1 FROM proposal_groups g WHERE g.id=pi.group_id AND NOT g.is_fixed)
	)`, id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM proposal_groups WHERE proposal_id=$1 AND NOT is_fixed`, id); err != nil {
		return nil, err
	}

	var startPosition, fixedOrders int
	if err := tx.QueryRow(ctx, `SELECT coalesce(max(position),0),count(pi.order_id)
		FROM proposal_groups g LEFT JOIN proposal_items pi ON pi.group_id=g.id
		WHERE g.proposal_id=$1 AND g.is_fixed`, id).Scan(&startPosition, &fixedOrders); err != nil {
		return nil, err
	}
	rows, err := tx.Query(ctx, `SELECT o.id,l.position IS NOT NULL AND NOT l.address_changed
		FROM service_orders o LEFT JOIN order_locations l ON l.order_id=o.id
		WHERE o.operation_id=$1 AND o.source=$2 AND o.normalized_status='open' AND o.scheduled_at IS NULL
		  AND NOT EXISTS (SELECT 1 FROM proposal_items pi WHERE pi.proposal_id=$3 AND pi.order_id=o.id)
		  AND NOT EXISTS (
			SELECT 1 FROM proposal_items pi JOIN planning_proposals p ON p.id=pi.proposal_id
			WHERE pi.order_id=o.id AND p.status IN ('draft','approved','conflicted') AND p.id<>$3
		  ) ORDER BY o.external_id`, operationID, source, id)
	if err != nil {
		return nil, err
	}
	candidates := make([]proposalCandidate, 0)
	positioned := make([]string, 0)
	for rows.Next() {
		var candidate proposalCandidate
		if err := rows.Scan(&candidate.ID, &candidate.HasPoint); err != nil {
			rows.Close()
			return nil, err
		}
		candidates = append(candidates, candidate)
		if candidate.HasPoint {
			positioned = append(positioned, candidate.ID)
		}
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	edgeRows, err := tx.Query(ctx, `SELECT a.order_id,b.order_id FROM order_locations a
		JOIN order_locations b ON a.order_id<b.order_id
		WHERE a.order_id=ANY($1::uuid[]) AND b.order_id=ANY($1::uuid[])
		  AND a.position IS NOT NULL AND b.position IS NOT NULL AND ST_DWithin(a.position,b.position,$2)`, positioned, radius)
	if err != nil {
		return nil, err
	}
	edges := make([][2]string, 0)
	for edgeRows.Next() {
		var edge [2]string
		if err := edgeRows.Scan(&edge[0], &edge[1]); err != nil {
			edgeRows.Close()
			return nil, err
		}
		edges = append(edges, edge)
	}
	edgeRows.Close()
	if err := edgeRows.Err(); err != nil {
		return nil, err
	}
	for index, orderIDs := range connectedComponents(positioned, edges) {
		position := startPosition + index + 1
		var groupID string
		if err := tx.QueryRow(ctx, `INSERT INTO proposal_groups (proposal_id,name,position)
			VALUES ($1,$2,$3) RETURNING id`, id, fmt.Sprintf("Grupo %d", position), position).Scan(&groupID); err != nil {
			return nil, err
		}
		for _, orderID := range orderIDs {
			if _, err := tx.Exec(ctx, `INSERT INTO proposal_items (proposal_id,order_id,group_id,observed_order_version,observed_location_version)
				SELECT $1,o.id,$2,o.observed_version,l.location_version FROM service_orders o LEFT JOIN order_locations l ON l.order_id=o.id WHERE o.id=$3`, id, groupID, orderID); err != nil {
				return nil, err
			}
		}
	}
	for _, candidate := range candidates {
		if candidate.HasPoint {
			continue
		}
		if _, err := tx.Exec(ctx, `INSERT INTO proposal_items (proposal_id,order_id,is_pending,observed_order_version,observed_location_version)
			SELECT $1,o.id,true,o.observed_version,l.location_version FROM service_orders o LEFT JOIN order_locations l ON l.order_id=o.id WHERE o.id=$2`, id, candidate.ID); err != nil {
			return nil, err
		}
	}
	if _, err := tx.Exec(ctx, `UPDATE planning_proposals SET status='draft',version=version+1,generator='deterministic-refresh',approved_at=NULL,updated_at=now() WHERE id=$1`, id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO events (aggregate_type,aggregate_id,event_type,metadata)
		VALUES ('planning_proposal',$1,'proposal.refreshed',jsonb_build_object('version',$2::integer,'fixed_orders',$3::integer,'recalculated_orders',$4::integer))`, id, expectedVersion+1, fixedOrders, len(candidates)); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.Proposal(ctx, id)
}

func (s *Store) ApproveProposal(ctx context.Context, id, source string, expectedVersion int) (*Proposal, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)
	var status string
	var version int
	var teamID, responsibleID *string
	if err := tx.QueryRow(ctx, `SELECT status,version,team_id,agenda_responsible_id FROM planning_proposals WHERE id=$1 AND source=$2 FOR UPDATE`, id, source).
		Scan(&status, &version, &teamID, &responsibleID); err != nil {
		return nil, err
	}
	if status != "draft" || version != expectedVersion {
		return nil, ErrProposalConflict
	}
	if teamID == nil || responsibleID == nil {
		return nil, fmt.Errorf("%w: equipe e responsável da agenda são obrigatórios", ErrInvalidProposal)
	}
	var changed int
	if err := tx.QueryRow(ctx, `SELECT count(*) FROM proposal_items pi JOIN service_orders o ON o.id=pi.order_id
		LEFT JOIN order_locations l ON l.order_id=o.id
		WHERE pi.proposal_id=$1 AND (o.observed_version<>pi.observed_order_version
		  OR l.location_version IS DISTINCT FROM pi.observed_location_version
		  OR o.normalized_status<>'open' OR o.scheduled_at IS NOT NULL OR coalesce(l.address_changed,false))`, id).Scan(&changed); err != nil {
		return nil, err
	}
	if changed > 0 {
		if _, err := tx.Exec(ctx, `UPDATE planning_proposals SET status='conflicted',version=version+1,updated_at=now() WHERE id=$1`, id); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `INSERT INTO events (aggregate_type,aggregate_id,event_type,metadata)
			VALUES ('planning_proposal',$1,'proposal.conflicted',jsonb_build_object('changed_orders',$2::integer))`, id, changed); err != nil {
			return nil, err
		}
		if err := tx.Commit(ctx); err != nil {
			return nil, err
		}
		return nil, ErrProposalConflict
	}
	if _, err := tx.Exec(ctx, `UPDATE planning_proposals SET status='approved',version=version+1,approved_at=now(),updated_at=now() WHERE id=$1`, id); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO events (aggregate_type,aggregate_id,event_type,metadata)
		VALUES ('planning_proposal',$1,'proposal.approved',jsonb_build_object('version',$2::integer))`, id, expectedVersion+1); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.Proposal(ctx, id)
}
