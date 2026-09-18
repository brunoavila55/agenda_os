package store

import (
	"context"
	"time"
)

type PreviewIssue struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type SchedulingPreviewItem struct {
	OrderID         string         `json:"order_id"`
	ExternalID      string         `json:"external_id"`
	CustomerDisplay string         `json:"customer_display"`
	Address         string         `json:"address"`
	CurrentStatus   string         `json:"current_status"`
	Issues          []PreviewIssue `json:"issues"`
}

type SchedulingPreview struct {
	ProposalID        string                  `json:"proposal_id"`
	ProposalVersion   int                     `json:"proposal_version"`
	ProposalStatus    string                  `json:"proposal_status"`
	Operation         string                  `json:"operation"`
	OperationalDate   string                  `json:"operational_date"`
	Team              *string                 `json:"team"`
	AgendaResponsible *string                 `json:"agenda_responsible"`
	Items             []SchedulingPreviewItem `json:"items"`
	Blockers          []PreviewIssue          `json:"blockers"`
	CanCreateRequests bool                    `json:"can_create_requests"`
}

func (s *Store) SchedulingPreview(ctx context.Context, proposalID, source string) (*SchedulingPreview, error) {
	var result SchedulingPreview
	err := s.Pool.QueryRow(ctx, `SELECT p.id,p.version,p.status,o.name,p.operational_date::text,t.name,tech.name
		FROM planning_proposals p JOIN operations o ON o.id=p.operation_id
		LEFT JOIN teams t ON t.id=p.team_id LEFT JOIN technicians tech ON tech.id=p.agenda_responsible_id
		WHERE p.id=$1 AND p.source=$2`, proposalID, source).Scan(&result.ProposalID, &result.ProposalVersion,
		&result.ProposalStatus, &result.Operation, &result.OperationalDate, &result.Team, &result.AgendaResponsible)
	if err != nil {
		return nil, err
	}
	result.Items = make([]SchedulingPreviewItem, 0)
	rows, err := s.Pool.Query(ctx, `SELECT o.id,o.external_id,o.customer_display,o.address_original,o.normalized_status,
		o.scheduled_at,o.observed_version,pi.observed_order_version,l.position IS NOT NULL,
		coalesce(l.address_changed,false),l.location_version,pi.observed_location_version
		FROM proposal_items pi JOIN service_orders o ON o.id=pi.order_id
		LEFT JOIN order_locations l ON l.order_id=o.id
		WHERE pi.proposal_id=$1 ORDER BY o.external_id`, proposalID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var item SchedulingPreviewItem
		var scheduledAt *time.Time
		var observedVersion, plannedVersion int64
		var hasPosition, addressChanged bool
		var locationVersion, plannedLocationVersion *int64
		if err := rows.Scan(&item.OrderID, &item.ExternalID, &item.CustomerDisplay, &item.Address,
			&item.CurrentStatus, &scheduledAt, &observedVersion, &plannedVersion, &hasPosition,
			&addressChanged, &locationVersion, &plannedLocationVersion); err != nil {
			return nil, err
		}
		item.Issues = make([]PreviewIssue, 0)
		if item.CurrentStatus != "open" || scheduledAt != nil {
			item.Issues = append(item.Issues, PreviewIssue{Code: "order_not_eligible", Message: "A ordem não está mais aberta e sem agendamento."})
		}
		if observedVersion != plannedVersion {
			item.Issues = append(item.Issues, PreviewIssue{Code: "order_changed", Message: "Os dados observados mudaram após a aprovação."})
		}
		if !equalOptionalInt64(locationVersion, plannedLocationVersion) || addressChanged {
			item.Issues = append(item.Issues, PreviewIssue{Code: "location_changed", Message: "A localização mudou ou precisa de nova revisão."})
		}
		if !hasPosition {
			item.Issues = append(item.Issues, PreviewIssue{Code: "location_missing", Message: "A ordem não possui posição confirmada."})
		}
		result.Items = append(result.Items, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	result.Blockers = []PreviewIssue{
		{Code: "daily_window_undefined", Message: "A convenção de início e fim do planejamento diário ainda não foi definida."},
		{Code: "schedule_conflicts_unavailable", Message: "A listagem de técnicos não permite verificar conflitos de horário no MK."},
		{Code: "mk_write_disabled", Message: "O envio real permanece bloqueado até a confirmação do contrato de agendamento."},
	}
	if result.ProposalStatus != "approved" {
		result.Blockers = append([]PreviewIssue{{Code: "proposal_not_approved", Message: "A proposta precisa estar aprovada para preparar comandos."}}, result.Blockers...)
	}
	result.CanCreateRequests = false
	return &result, nil
}

func equalOptionalInt64(current, planned *int64) bool {
	if current == nil || planned == nil {
		return current == nil && planned == nil
	}
	return *current == *planned
}
