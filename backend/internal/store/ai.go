package store

import (
	"context"
	"encoding/json"
	"errors"

	"github.com/jackc/pgx/v5"
)

// rowQuerier is the common subset of *pgxpool.Pool and pgx.Tx. Recording a
// grouping suggestion must run on whichever one the caller already holds:
// inside the same transaction that applies a validated suggestion (so the
// version check below sees the caller's own uncommitted row), or directly on
// the pool when only recording a failure after the network call returned.
type rowQuerier interface {
	QueryRow(ctx context.Context, sql string, args ...any) pgx.Row
}

func (s *Store) RecordGroupingSuggestion(ctx context.Context, proposalID, source string, proposalVersion int,
	model, promptVersion, inputSignature, status string, result any, sanitizedError string) (string, error) {
	return recordGroupingSuggestion(ctx, s.Pool, proposalID, source, proposalVersion, model, promptVersion, inputSignature, status, result, sanitizedError)
}

func recordGroupingSuggestion(ctx context.Context, exec rowQuerier, proposalID, source string, proposalVersion int,
	model, promptVersion, inputSignature, status string, result any, sanitizedError string) (string, error) {
	var encodedResult []byte
	var err error
	if result != nil {
		encodedResult, err = json.Marshal(result)
		if err != nil {
			return "", err
		}
	}
	if len(sanitizedError) > 500 {
		sanitizedError = sanitizedError[:500]
	}
	var id string
	err = exec.QueryRow(ctx, `INSERT INTO grouping_suggestions (
		proposal_id,proposal_version,provider,model,prompt_version,input_signature,status,validated_result,sanitized_error
	)
	SELECT p.id,$3,'cloudflare_workers_ai',$4,$5,$6,$7,$8::jsonb,nullif($9,'')
	FROM planning_proposals p WHERE p.id=$1 AND p.source=$2 AND p.version=$3
	RETURNING id`, proposalID, source, proposalVersion, model, promptVersion, inputSignature, status, encodedResult, sanitizedError).Scan(&id)
	if errors.Is(err, pgx.ErrNoRows) {
		return "", ErrProposalConflict
	}
	return id, err
}
