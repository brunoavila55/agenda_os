package store

import (
	"context"
	"errors"

	"agendaos/internal/llm"

	"github.com/jackc/pgx/v5"
)

// GroupingSuggester is the substitutable boundary to Workers AI (AGENTS.md
// §13: it only suggests groupings, never decides eligibility). *llm.Client
// implements it; tests use a fake to exercise the fallback and application
// paths below without a network call.
type GroupingSuggester interface {
	Model() string
	Suggest(ctx context.Context, input llm.Input) (llm.Suggestion, error)
}

const groupingPromptVersion = "v2"

// SuggestGrouping asks suggester to regroup the still-adjustable part of a
// draft proposal — positioned orders that are pending or sit in a non-fixed
// group — and applies the result if it validates. It never touches fixed
// groups or approved proposals, never holds a database transaction open
// across the network call (AGENTS.md §11), and on any failure — network,
// malformed output, or the proposal changing while the call was in flight —
// leaves the deterministic proposal exactly as it was: the caller always
// gets back a usable proposal, never an error caused by the AI being
// unavailable.
func (s *Store) SuggestGrouping(ctx context.Context, proposalID, source string, suggester GroupingSuggester) (*Proposal, error) {
	var status string
	var version, radius int
	err := s.Pool.QueryRow(ctx, `SELECT p.status, p.version, o.grouping_radius_meters
		FROM planning_proposals p JOIN operations o ON o.id = p.operation_id
		WHERE p.id = $1 AND p.source = $2`, proposalID, source).Scan(&status, &version, &radius)
	if err != nil {
		return nil, err
	}
	if status != "draft" {
		return s.Proposal(ctx, proposalID)
	}

	rows, err := s.Pool.Query(ctx, `
		SELECT o.id, ST_Y(l.position::geometry), ST_X(l.position::geometry)
		FROM proposal_items pi
		JOIN service_orders o ON o.id = pi.order_id
		JOIN order_locations l ON l.order_id = o.id
		LEFT JOIN proposal_groups g ON g.id = pi.group_id
		WHERE pi.proposal_id = $1 AND l.position IS NOT NULL
		  AND (pi.is_pending OR (g.id IS NOT NULL AND NOT g.is_fixed))`, proposalID)
	if err != nil {
		return nil, err
	}
	input := llm.Input{RadiusMeters: radius}
	ids := make([]string, 0)
	for rows.Next() {
		var order llm.Order
		var lat, lng float64
		if err := rows.Scan(&order.ID, &lat, &lng); err != nil {
			rows.Close()
			return nil, err
		}
		order.Latitude, order.Longitude = &lat, &lng
		input.Orders = append(input.Orders, order)
		ids = append(ids, order.ID)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(input.Orders) < 2 {
		// Nothing meaningful to regroup; asking the model would just cost a
		// call for a result the deterministic path already produces.
		return s.Proposal(ctx, proposalID)
	}

	distanceRows, err := s.Pool.Query(ctx, `
		SELECT a.order_id, b.order_id, ST_Distance(a.position, b.position)
		FROM order_locations a JOIN order_locations b ON a.order_id < b.order_id
		WHERE a.order_id = ANY($1::uuid[]) AND b.order_id = ANY($1::uuid[])
		  AND ST_DWithin(a.position, b.position, $2)`, ids, radius)
	if err != nil {
		return nil, err
	}
	for distanceRows.Next() {
		var distance llm.Distance
		if err := distanceRows.Scan(&distance.FromID, &distance.ToID, &distance.Meters); err != nil {
			distanceRows.Close()
			return nil, err
		}
		input.Distances = append(input.Distances, distance)
	}
	distanceRows.Close()
	if err := distanceRows.Err(); err != nil {
		return nil, err
	}

	signature, err := llm.InputSignature(input)
	if err != nil {
		return nil, err
	}

	suggestion, suggestErr := suggester.Suggest(ctx, input)
	if suggestErr == nil {
		// The client already validates before returning, but the backend
		// must never trust a substitutable dependency to have done so
		// (AGENTS.md §13): validate again regardless of the implementation.
		suggestErr = llm.Validate(input, suggestion)
	}
	if suggestErr != nil {
		message := suggestErr.Error()
		if len(message) > 500 {
			message = message[:500]
		}
		if _, err := s.RecordGroupingSuggestion(ctx, proposalID, source, version, suggester.Model(), groupingPromptVersion, signature, "failed", nil, message); err != nil && !errors.Is(err, ErrProposalConflict) {
			return nil, err
		}
		return s.Proposal(ctx, proposalID)
	}

	return s.applyGroupingSuggestion(ctx, proposalID, source, version, suggester.Model(), signature, suggestion)
}

// applyGroupingSuggestion writes a validated suggestion, but only if the
// proposal is still exactly the draft version the suggestion was computed
// from. If a manual edit or another refresh landed while the network call
// was in flight, it is a no-op: the caller's own state already reflects the
// newer change and must not be clobbered by a now-stale AI answer.
func (s *Store) applyGroupingSuggestion(ctx context.Context, proposalID, source string, expectedVersion int, model, signature string, suggestion llm.Suggestion) (*Proposal, error) {
	tx, err := s.Pool.BeginTx(ctx, pgx.TxOptions{IsoLevel: pgx.Serializable})
	if err != nil {
		return nil, err
	}
	defer tx.Rollback(ctx)

	var status string
	var version int
	if err := tx.QueryRow(ctx, `SELECT status, version FROM planning_proposals WHERE id = $1 AND source = $2 FOR UPDATE`,
		proposalID, source).Scan(&status, &version); err != nil {
		return nil, err
	}
	if status != "draft" || version != expectedVersion {
		return s.Proposal(ctx, proposalID)
	}

	var startPosition int
	if err := tx.QueryRow(ctx, `SELECT coalesce(max(position), 0) FROM proposal_groups WHERE proposal_id = $1 AND is_fixed`,
		proposalID).Scan(&startPosition); err != nil {
		return nil, err
	}
	// Same ordering as UpdateProposal/RefreshProposal: every affected item
	// must become valid-and-pending before its group row is deleted, because
	// the check constraint on proposal_items is immediate and group_id
	// references proposal_groups ON DELETE CASCADE.
	if _, err := tx.Exec(ctx, `UPDATE proposal_items pi SET group_id = NULL, is_pending = true
		WHERE pi.proposal_id = $1 AND EXISTS (
			SELECT 1 FROM proposal_groups g WHERE g.id = pi.group_id AND NOT g.is_fixed
		)`, proposalID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `DELETE FROM proposal_groups WHERE proposal_id = $1 AND NOT is_fixed`, proposalID); err != nil {
		return nil, err
	}

	for index, group := range suggestion.Groups {
		position := startPosition + index + 1
		var groupID string
		if err := tx.QueryRow(ctx, `INSERT INTO proposal_groups (proposal_id, name, position) VALUES ($1, $2, $3) RETURNING id`,
			proposalID, group.Name, position).Scan(&groupID); err != nil {
			return nil, err
		}
		if _, err := tx.Exec(ctx, `UPDATE proposal_items SET group_id = $2, is_pending = false
			WHERE proposal_id = $1 AND order_id = ANY($3::uuid[])`, proposalID, groupID, group.OrderIDs); err != nil {
			return nil, err
		}
	}
	// suggestion.PendingOrderIDs need no separate write: the reset above
	// already left them group_id = NULL, is_pending = true, and llm.Validate
	// guarantees every id sent is accounted for in groups or pending_order_ids.

	if _, err := recordGroupingSuggestion(ctx, tx, proposalID, source, expectedVersion, model, groupingPromptVersion, signature, "validated", suggestion, ""); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `UPDATE planning_proposals SET generator = 'workers_ai', version = version + 1, updated_at = now()
		WHERE id = $1`, proposalID); err != nil {
		return nil, err
	}
	if _, err := tx.Exec(ctx, `INSERT INTO events (aggregate_type, aggregate_id, event_type, metadata)
		VALUES ('planning_proposal', $1, 'proposal.ai_grouping_applied', jsonb_build_object('version', $2::integer, 'groups', $3::integer))`,
		proposalID, expectedVersion+1, len(suggestion.Groups)); err != nil {
		return nil, err
	}
	if err := tx.Commit(ctx); err != nil {
		return nil, err
	}
	return s.Proposal(ctx, proposalID)
}
