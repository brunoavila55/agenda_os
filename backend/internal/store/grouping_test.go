// External store_test package: SuggestGrouping needs real PostgreSQL/PostGIS
// via testdb, which itself depends on store — see jobs_test.go for why an
// internal test file can't import testdb.
package store_test

import (
	"context"
	"sort"
	"testing"
	"time"

	"agendaos/internal/llm"
	"agendaos/internal/store"
	"agendaos/internal/testdb"
)

func serviceTypeID(t *testing.T, ctx context.Context, s *store.Store, operationID string) string {
	t.Helper()
	var id string
	if err := s.Pool.QueryRow(ctx, `SELECT service_type_id FROM operation_service_types WHERE operation_id = $1 LIMIT 1`, operationID).Scan(&id); err != nil {
		t.Fatalf("obter tipo semeado: %v", err)
	}
	return id
}

func insertPositionedOrder(t *testing.T, ctx context.Context, s *store.Store, operationID, typeID, externalID string, lat, lng float64) string {
	t.Helper()
	var orderID string
	err := s.Pool.QueryRow(ctx, `
		INSERT INTO service_orders (source, external_id, operation_id, service_type_id, source_status_code, normalized_status, customer_display, address_original, observed_at)
		VALUES ('simulation', $1, $2, $3, 'AB', 'open', $1, $1, now())
		RETURNING id`, externalID, operationID, typeID).Scan(&orderID)
	if err != nil {
		t.Fatalf("inserir ordem de teste: %v", err)
	}
	if _, err := s.Pool.Exec(ctx, `
		INSERT INTO order_locations (order_id, position, source, confidence, address_fingerprint, observed_at)
		VALUES ($1, ST_SetSRID(ST_MakePoint($2,$3),4326)::geography, 'fixture', 1, $4, now())`,
		orderID, lng, lat, externalID); err != nil {
		t.Fatalf("inserir localização de teste: %v", err)
	}
	return orderID
}

func orderIDs(orders []store.ProposalOrder) []string {
	ids := make([]string, len(orders))
	for i, order := range orders {
		ids[i] = order.ID
	}
	sort.Strings(ids)
	return ids
}

func llmOrderIDs(orders []llm.Order) []string {
	ids := make([]string, len(orders))
	for i, order := range orders {
		ids[i] = order.ID
	}
	sort.Strings(ids)
	return ids
}

func findGroup(t *testing.T, proposal *store.Proposal, name string) store.ProposalGroup {
	t.Helper()
	for _, group := range proposal.Groups {
		if group.Name == name {
			return group
		}
	}
	t.Fatalf("grupo %q não encontrado; grupos=%+v", name, proposal.Groups)
	return store.ProposalGroup{}
}

func countGroupingSuggestions(t *testing.T, ctx context.Context, s *store.Store, proposalID, status string) int {
	t.Helper()
	var count int
	if err := s.Pool.QueryRow(ctx, `SELECT count(*) FROM grouping_suggestions WHERE proposal_id = $1 AND status = $2`,
		proposalID, status).Scan(&count); err != nil {
		t.Fatalf("contar grouping_suggestions: %v", err)
	}
	return count
}

// fakeSuggester is the substitutable Workers AI boundary used to exercise
// SuggestGrouping without a network call: it can return a canned suggestion,
// a canned error, and it records the exact input it received so tests can
// assert what was (or wasn't) sent to it.
type fakeSuggester struct {
	suggestion   llm.Suggestion
	err          error
	beforeReturn func()
	calls        int
	lastInput    llm.Input
}

func (f *fakeSuggester) Model() string { return "@cf/test/fake" }

func (f *fakeSuggester) Suggest(_ context.Context, input llm.Input) (llm.Suggestion, error) {
	f.calls++
	f.lastInput = input
	if f.beforeReturn != nil {
		f.beforeReturn()
	}
	if f.err != nil {
		return llm.Suggestion{}, f.err
	}
	return f.suggestion, nil
}

func TestSuggestGrouping_AppliesValidatedSuggestion(t *testing.T) {
	s := testdb.New(t)
	ctx := context.Background()
	operationID := ruralOperationID(t, ctx, s)
	typeID := serviceTypeID(t, ctx, s, operationID)

	orderA := insertPositionedOrder(t, ctx, s, operationID, typeID, "AI-A", -23.500, -46.800)
	orderB := insertPositionedOrder(t, ctx, s, operationID, typeID, "AI-B", -23.501, -46.801)
	orderC := insertPositionedOrder(t, ctx, s, operationID, typeID, "AI-C", -23.503, -46.803)

	proposal, created, err := s.CreateProposal(ctx, operationID, time.Now(), "simulation")
	if err != nil || !created {
		t.Fatalf("CreateProposal: proposal=%+v created=%v err=%v", proposal, created, err)
	}
	if proposal.Generator != "deterministic" {
		t.Fatalf("generator inicial = %q, want deterministic", proposal.Generator)
	}
	if got := orderIDs(proposal.Groups[0].Orders); len(proposal.Groups) != 1 || len(got) != 3 {
		t.Fatalf("agrupamento determinístico inesperado: %+v", proposal.Groups)
	}

	fake := &fakeSuggester{suggestion: llm.Suggestion{
		Groups:          []llm.Group{{Name: "Sugestão IA", OrderIDs: []string{orderA, orderB}}},
		PendingOrderIDs: []string{orderC},
	}}

	updated, err := s.SuggestGrouping(ctx, proposal.ID, "simulation", fake)
	if err != nil {
		t.Fatalf("SuggestGrouping: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("chamadas ao suggester = %d, want 1", fake.calls)
	}
	if updated.Generator != "workers_ai" {
		t.Fatalf("generator = %q, want workers_ai", updated.Generator)
	}
	group := findGroup(t, updated, "Sugestão IA")
	wantAB := []string{orderA, orderB}
	sort.Strings(wantAB)
	if got := orderIDs(group.Orders); !equalStrings(got, wantAB) {
		t.Fatalf("grupo sugerido = %+v, want %+v", got, wantAB)
	}
	if len(updated.Pending) != 1 || updated.Pending[0].ID != orderC {
		t.Fatalf("pendências = %+v, want [%s]", updated.Pending, orderC)
	}
	if countGroupingSuggestions(t, ctx, s, proposal.ID, "validated") != 1 {
		t.Fatalf("esperava um registro de sugestão validada")
	}
}

func TestSuggestGrouping_FallsBackWhenSuggestionInvalid(t *testing.T) {
	s := testdb.New(t)
	ctx := context.Background()
	operationID := ruralOperationID(t, ctx, s)
	typeID := serviceTypeID(t, ctx, s, operationID)

	insertPositionedOrder(t, ctx, s, operationID, typeID, "INV-A", -23.500, -46.800)
	insertPositionedOrder(t, ctx, s, operationID, typeID, "INV-B", -23.501, -46.801)
	insertPositionedOrder(t, ctx, s, operationID, typeID, "INV-C", -23.503, -46.803)

	proposal, created, err := s.CreateProposal(ctx, operationID, time.Now(), "simulation")
	if err != nil || !created {
		t.Fatalf("CreateProposal: proposal=%+v created=%v err=%v", proposal, created, err)
	}
	originalGroup := orderIDs(proposal.Groups[0].Orders)

	// Omits one of the three ids entirely: llm.Validate must reject this.
	fake := &fakeSuggester{suggestion: llm.Suggestion{
		Groups: []llm.Group{{Name: "Incompleto", OrderIDs: []string{proposal.Groups[0].Orders[0].ID, proposal.Groups[0].Orders[1].ID}}},
	}}

	updated, err := s.SuggestGrouping(ctx, proposal.ID, "simulation", fake)
	if err != nil {
		t.Fatalf("SuggestGrouping não deveria falhar a chamada: %v", err)
	}
	if updated.Generator != "deterministic" {
		t.Fatalf("generator = %q, want deterministic (sem alteração)", updated.Generator)
	}
	if len(updated.Groups) != 1 || !equalStrings(orderIDs(updated.Groups[0].Orders), originalGroup) {
		t.Fatalf("agrupamento determinístico foi alterado: %+v", updated.Groups)
	}
	if countGroupingSuggestions(t, ctx, s, proposal.ID, "failed") != 1 {
		t.Fatalf("esperava um registro de sugestão com falha")
	}
}

func TestSuggestGrouping_NeverTouchesFixedGroup(t *testing.T) {
	s := testdb.New(t)
	ctx := context.Background()
	operationID := ruralOperationID(t, ctx, s)
	typeID := serviceTypeID(t, ctx, s, operationID)

	// Cluster 1 (~150m apart) and cluster 2 (~26km from cluster 1, far
	// beyond the seeded 12km radius) form two separate deterministic groups.
	insertPositionedOrder(t, ctx, s, operationID, typeID, "FX-A", -23.500, -46.800)
	insertPositionedOrder(t, ctx, s, operationID, typeID, "FX-B", -23.501, -46.801)
	orderC := insertPositionedOrder(t, ctx, s, operationID, typeID, "FX-C", -23.700, -46.950)
	orderD := insertPositionedOrder(t, ctx, s, operationID, typeID, "FX-D", -23.701, -46.951)

	proposal, created, err := s.CreateProposal(ctx, operationID, time.Now(), "simulation")
	if err != nil || !created || len(proposal.Groups) != 2 {
		t.Fatalf("CreateProposal: proposal=%+v created=%v err=%v", proposal, created, err)
	}
	var fixedGroup, freeGroup store.ProposalGroup
	for _, group := range proposal.Groups {
		if containsID(orderIDs(group.Orders), orderC) {
			freeGroup = group
		} else {
			fixedGroup = group
		}
	}
	if fixedGroup.ID == "" || freeGroup.ID == "" {
		t.Fatalf("não foi possível separar os grupos: %+v", proposal.Groups)
	}

	groupsInput := []store.ProposalGroupInput{
		{Name: fixedGroup.Name, IsFixed: true, OrderIDs: orderIDs(fixedGroup.Orders)},
		{Name: freeGroup.Name, IsFixed: false, OrderIDs: orderIDs(freeGroup.Orders)},
	}
	if _, err := s.UpdateProposal(ctx, proposal.ID, "simulation", proposal.Version, nil, nil, groupsInput, orderIDs(proposal.Pending)); err != nil {
		t.Fatalf("UpdateProposal (fixar grupo): %v", err)
	}

	fake := &fakeSuggester{suggestion: llm.Suggestion{
		Groups: []llm.Group{{Name: "IA Grupo", OrderIDs: []string{orderC, orderD}}},
	}}
	updated, err := s.SuggestGrouping(ctx, proposal.ID, "simulation", fake)
	if err != nil {
		t.Fatalf("SuggestGrouping: %v", err)
	}
	if fake.calls != 1 {
		t.Fatalf("chamadas ao suggester = %d, want 1", fake.calls)
	}
	if got := llmOrderIDs(fake.lastInput.Orders); !equalStrings(got, orderIDs(freeGroup.Orders)) {
		t.Fatalf("entrada enviada ao suggester = %+v, want somente o grupo não fixado %+v", got, orderIDs(freeGroup.Orders))
	}
	stillFixed := findGroup(t, updated, fixedGroup.Name)
	if !stillFixed.IsFixed || !equalStrings(orderIDs(stillFixed.Orders), orderIDs(fixedGroup.Orders)) {
		t.Fatalf("grupo fixado foi alterado: %+v", stillFixed)
	}
	wantCD := []string{orderC, orderD}
	sort.Strings(wantCD)
	aiGroup := findGroup(t, updated, "IA Grupo")
	if !equalStrings(orderIDs(aiGroup.Orders), wantCD) {
		t.Fatalf("grupo sugerido pela IA = %+v, want %+v", orderIDs(aiGroup.Orders), wantCD)
	}
	if updated.Generator != "workers_ai" {
		t.Fatalf("generator = %q, want workers_ai", updated.Generator)
	}
}

func TestSuggestGrouping_SkipsWhenFewerThanTwoPositionedCandidates(t *testing.T) {
	s := testdb.New(t)
	ctx := context.Background()
	operationID := ruralOperationID(t, ctx, s)
	typeID := serviceTypeID(t, ctx, s, operationID)
	insertPositionedOrder(t, ctx, s, operationID, typeID, "SOLO-A", -23.500, -46.800)

	proposal, created, err := s.CreateProposal(ctx, operationID, time.Now(), "simulation")
	if err != nil || !created {
		t.Fatalf("CreateProposal: proposal=%+v created=%v err=%v", proposal, created, err)
	}

	fake := &fakeSuggester{suggestion: llm.Suggestion{Groups: []llm.Group{{Name: "Não deveria ser usado", OrderIDs: []string{"x"}}}}}
	updated, err := s.SuggestGrouping(ctx, proposal.ID, "simulation", fake)
	if err != nil {
		t.Fatalf("SuggestGrouping: %v", err)
	}
	if fake.calls != 0 {
		t.Fatalf("suggester não deveria ser chamado com menos de duas ordens posicionáveis, calls=%d", fake.calls)
	}
	if updated.Generator != "deterministic" {
		t.Fatalf("generator = %q, want deterministic", updated.Generator)
	}
}

func TestSuggestGrouping_ConcurrentEditDuringNetworkCallIsIgnored(t *testing.T) {
	s := testdb.New(t)
	ctx := context.Background()
	operationID := ruralOperationID(t, ctx, s)
	typeID := serviceTypeID(t, ctx, s, operationID)
	insertPositionedOrder(t, ctx, s, operationID, typeID, "RACE-A", -23.500, -46.800)
	insertPositionedOrder(t, ctx, s, operationID, typeID, "RACE-B", -23.501, -46.801)
	insertPositionedOrder(t, ctx, s, operationID, typeID, "RACE-C", -23.503, -46.803)

	proposal, created, err := s.CreateProposal(ctx, operationID, time.Now(), "simulation")
	if err != nil || !created {
		t.Fatalf("CreateProposal: proposal=%+v created=%v err=%v", proposal, created, err)
	}
	originalGroups := orderIDs(proposal.Groups[0].Orders)

	fake := &fakeSuggester{
		suggestion: llm.Suggestion{Groups: []llm.Group{{Name: "Tarde demais", OrderIDs: originalGroups}}},
		beforeReturn: func() {
			// Simulates a concurrent edit landing while the network call to
			// Workers AI was in flight — SuggestGrouping never holds a
			// database transaction open during that call, so this succeeds
			// immediately instead of blocking on a lock.
			if _, err := s.Pool.Exec(ctx, `UPDATE planning_proposals SET version = version + 1 WHERE id = $1`, proposal.ID); err != nil {
				t.Fatalf("simular edição concorrente: %v", err)
			}
		},
	}

	updated, err := s.SuggestGrouping(ctx, proposal.ID, "simulation", fake)
	if err != nil {
		t.Fatalf("SuggestGrouping: %v", err)
	}
	if updated.Generator != "deterministic" {
		t.Fatalf("generator = %q, want deterministic (sugestão tardia deve ser descartada)", updated.Generator)
	}
	if updated.Version != proposal.Version+1 {
		t.Fatalf("version = %d, want %d (apenas a edição concorrente)", updated.Version, proposal.Version+1)
	}
	if !equalStrings(orderIDs(updated.Groups[0].Orders), originalGroups) {
		t.Fatalf("agrupamento foi alterado por uma sugestão desatualizada: %+v", updated.Groups)
	}
	if countGroupingSuggestions(t, ctx, s, proposal.ID, "validated") != 0 {
		t.Fatalf("sugestão descartada por conflito não deveria ser registrada como validada")
	}
}

func containsID(ids []string, id string) bool {
	for _, candidate := range ids {
		if candidate == id {
			return true
		}
	}
	return false
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
