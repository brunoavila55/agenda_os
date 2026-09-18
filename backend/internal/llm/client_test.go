package llm

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func pointer(value float64) *float64 { return &value }

func TestValidateSuggestion(t *testing.T) {
	input := Input{RadiusMeters: 1000, Orders: []Order{
		{ID: "a", Latitude: pointer(-23), Longitude: pointer(-46)},
		{ID: "b", Latitude: pointer(-23.01), Longitude: pointer(-46.01)},
		{ID: "c"},
	}, Distances: []Distance{{FromID: "a", ToID: "b", Meters: 900}}}
	valid := Suggestion{Groups: []Group{{Name: "Grupo 1", OrderIDs: []string{"a", "b"}}}, PendingOrderIDs: []string{"c"}}
	if err := Validate(input, valid); err != nil {
		t.Fatalf("saída válida rejeitada: %v", err)
	}
	invalid := []Suggestion{
		{Groups: []Group{{Name: "Grupo", OrderIDs: []string{"a", "invented"}}}, PendingOrderIDs: []string{"b", "c"}},
		{Groups: []Group{{Name: "Grupo", OrderIDs: []string{"a"}}}, PendingOrderIDs: []string{"a", "b", "c"}},
		{Groups: []Group{{Name: "Grupo", OrderIDs: []string{"a", "b"}}}},
		{Groups: []Group{{Name: "Grupo", OrderIDs: []string{"c"}}}, PendingOrderIDs: []string{"a", "b"}},
	}
	for index, suggestion := range invalid {
		if err := Validate(input, suggestion); err == nil {
			t.Errorf("caso inválido %d aceito", index)
		}
	}
}

func TestValidateRejectsDisconnectedGroup(t *testing.T) {
	input := Input{RadiusMeters: 100, Orders: []Order{
		{ID: "a", Latitude: pointer(1), Longitude: pointer(1)},
		{ID: "b", Latitude: pointer(2), Longitude: pointer(2)},
	}}
	suggestion := Suggestion{Groups: []Group{{Name: "Distante", OrderIDs: []string{"a", "b"}}}}
	if err := Validate(input, suggestion); err == nil {
		t.Fatal("grupo desconectado foi aceito")
	}
}

func TestClientUsesStructuredOutputAndValidatesResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/accounts/account/ai/run/@cf/example/model" {
			t.Errorf("path = %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer secret" {
			t.Error("autorização ausente")
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		format, ok := body["response_format"].(map[string]any)
		if !ok || format["type"] != "json_schema" {
			t.Errorf("response_format = %#v", body["response_format"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"success":true,"result":{"response":{"groups":[{"name":"Grupo 1","order_ids":["a"]}],"pending_order_ids":["b"]}}}`))
	}))
	defer server.Close()
	client, err := New("account", "secret", "@cf/example/model", server.Client())
	if err != nil {
		t.Fatal(err)
	}
	client.baseURL = server.URL
	input := Input{RadiusMeters: 1000, Orders: []Order{{ID: "a", Latitude: pointer(1), Longitude: pointer(1)}, {ID: "b"}}}
	suggestion, err := client.Suggest(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if len(suggestion.Groups) != 1 || len(suggestion.PendingOrderIDs) != 1 {
		t.Fatalf("suggestion = %#v", suggestion)
	}
}
