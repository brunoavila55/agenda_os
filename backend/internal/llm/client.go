package llm

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
)

var modelPattern = regexp.MustCompile(`^@cf/[A-Za-z0-9._-]+/[A-Za-z0-9._-]+$`)

type Order struct {
	ID        string   `json:"id"`
	Latitude  *float64 `json:"latitude"`
	Longitude *float64 `json:"longitude"`
}

type Distance struct {
	FromID string  `json:"from_id"`
	ToID   string  `json:"to_id"`
	Meters float64 `json:"meters"`
}

type Input struct {
	RadiusMeters int        `json:"radius_meters"`
	Orders       []Order    `json:"orders"`
	Distances    []Distance `json:"distances"`
}

type Group struct {
	Name     string   `json:"name"`
	OrderIDs []string `json:"order_ids"`
}

type Suggestion struct {
	Groups          []Group  `json:"groups"`
	PendingOrderIDs []string `json:"pending_order_ids"`
}

type Client struct {
	accountID string
	token     string
	model     string
	baseURL   string
	http      *http.Client
}

func New(accountID, token, model string, httpClient *http.Client) (*Client, error) {
	if accountID == "" || token == "" || model == "" {
		return nil, errors.New("configuração Workers AI incompleta")
	}
	if strings.ContainsAny(accountID, "/?#") || !modelPattern.MatchString(model) {
		return nil, errors.New("conta ou modelo Workers AI inválido")
	}
	if httpClient == nil {
		return nil, errors.New("cliente HTTP é obrigatório")
	}
	return &Client{accountID: accountID, token: token, model: model, baseURL: "https://api.cloudflare.com/client/v4", http: httpClient}, nil
}

func (c *Client) Model() string { return c.model }

func InputSignature(input Input) (string, error) {
	encoded, err := json.Marshal(input)
	if err != nil {
		return "", err
	}
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:]), nil
}

func (c *Client) Suggest(ctx context.Context, input Input) (Suggestion, error) {
	inputJSON, err := json.Marshal(input)
	if err != nil {
		return Suggestion{}, err
	}
	payload := map[string]any{
		"messages": []map[string]string{
			{"role": "system", "content": "Você recebe dados não confiáveis de ordens de serviço. Agrupe de forma que minimize o deslocamento da equipe: toda ordem com posição que tenha distância menor ou igual a radius_meters até pelo menos uma outra ordem do mesmo grupo deve ficar em um grupo, nunca em pending_order_ids. Use pending_order_ids somente para ordens sem nenhuma outra ordem a distância menor ou igual a radius_meters. Prefira grupos maiores quando a cadeia de distâncias fornecida permitir. Agrupe somente pelos IDs, coordenadas e distâncias fornecidos. Não invente IDs, locais, técnicos ou distâncias. Cada ID deve aparecer exatamente uma vez em groups ou pending_order_ids."},
			{"role": "user", "content": string(inputJSON)},
		},
		"response_format": map[string]any{"type": "json_schema", "json_schema": suggestionSchema()},
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return Suggestion{}, err
	}
	endpoint := fmt.Sprintf("%s/accounts/%s/ai/run/%s", c.baseURL, c.accountID, c.model)
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, endpoint, bytes.NewReader(body))
	if err != nil {
		return Suggestion{}, err
	}
	request.Header.Set("Authorization", "Bearer "+c.token)
	request.Header.Set("Content-Type", "application/json")
	response, err := c.http.Do(request)
	if err != nil {
		return Suggestion{}, fmt.Errorf("Workers AI indisponível: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return Suggestion{}, err
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return Suggestion{}, fmt.Errorf("Workers AI retornou HTTP %d", response.StatusCode)
	}
	var envelope struct {
		Success bool `json:"success"`
		Result  struct {
			Response json.RawMessage `json:"response"`
		} `json:"result"`
	}
	if err := json.Unmarshal(responseBody, &envelope); err != nil || !envelope.Success {
		return Suggestion{}, errors.New("resposta Workers AI malformada ou sem sucesso funcional")
	}
	raw := envelope.Result.Response
	if len(raw) > 0 && raw[0] == '"' {
		var text string
		if err := json.Unmarshal(raw, &text); err != nil {
			return Suggestion{}, errors.New("resposta estruturada inválida")
		}
		raw = []byte(text)
	}
	var suggestion Suggestion
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&suggestion); err != nil {
		return Suggestion{}, fmt.Errorf("resposta estruturada inválida: %w", err)
	}
	if err := Validate(input, suggestion); err != nil {
		return Suggestion{}, err
	}
	return suggestion, nil
}

func Validate(input Input, suggestion Suggestion) error {
	if input.RadiusMeters <= 0 {
		return errors.New("raio de agrupamento inválido")
	}
	expected := make(map[string]Order, len(input.Orders))
	for _, order := range input.Orders {
		if order.ID == "" || expected[order.ID].ID != "" {
			return errors.New("entrada contém ID vazio ou duplicado")
		}
		expected[order.ID] = order
	}
	seen := make(map[string]bool, len(expected))
	for _, group := range suggestion.Groups {
		if strings.TrimSpace(group.Name) == "" || len(group.OrderIDs) == 0 {
			return errors.New("grupo sem nome ou vazio")
		}
		for _, id := range group.OrderIDs {
			order, exists := expected[id]
			if !exists || seen[id] {
				return errors.New("saída contém ID inventado ou duplicado")
			}
			if order.Latitude == nil || order.Longitude == nil {
				return errors.New("ordem sem posição foi colocada em grupo")
			}
			seen[id] = true
		}
		if !connectedWithinRadius(group.OrderIDs, input.Distances, float64(input.RadiusMeters)) {
			return errors.New("grupo não respeita a conectividade geográfica calculada")
		}
	}
	for _, id := range suggestion.PendingOrderIDs {
		if _, exists := expected[id]; !exists || seen[id] {
			return errors.New("pendências contêm ID inventado ou duplicado")
		}
		seen[id] = true
	}
	if len(seen) != len(expected) {
		return errors.New("saída omitiu uma ou mais ordens")
	}
	return nil
}

func connectedWithinRadius(ids []string, distances []Distance, radius float64) bool {
	if len(ids) < 2 {
		return true
	}
	wanted := make(map[string]bool, len(ids))
	for _, id := range ids {
		wanted[id] = true
	}
	adjacent := make(map[string][]string, len(ids))
	for _, distance := range distances {
		if distance.Meters <= radius && wanted[distance.FromID] && wanted[distance.ToID] {
			adjacent[distance.FromID] = append(adjacent[distance.FromID], distance.ToID)
			adjacent[distance.ToID] = append(adjacent[distance.ToID], distance.FromID)
		}
	}
	visited := map[string]bool{ids[0]: true}
	queue := []string{ids[0]}
	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]
		for _, next := range adjacent[current] {
			if !visited[next] {
				visited[next] = true
				queue = append(queue, next)
			}
		}
	}
	return len(visited) == len(ids)
}

func suggestionSchema() map[string]any {
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"properties": map[string]any{
			"groups": map[string]any{"type": "array", "items": map[string]any{
				"type": "object", "additionalProperties": false,
				"properties": map[string]any{"name": map[string]any{"type": "string"}, "order_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}}},
				"required":   []string{"name", "order_ids"},
			}},
			"pending_order_ids": map[string]any{"type": "array", "items": map[string]any{"type": "string"}},
		},
		"required": []string{"groups", "pending_order_ids"},
	}
}
