// Package mk implementa o cliente HTTP do ERP MK Solutions. Nenhuma chamada
// ao MK deve acontecer fora deste pacote (AGENTS.md §8).
//
// O contrato de WSAutenticacao.rule e de WSMKOSListaTiposOS.rule abaixo foi
// confirmado contra o MK real de produção (sac.newlifefibra.com.br, em
// 2026-09-18): GET com sys/token/password/cd_servico na query string, nunca
// corpo JSON; a lista de tipos vem em {"Tipos":[{"codostipo":<int>,
// "descricao":<string>}],"status":"OK"}. Os demais endpoints do MK (equipes,
// técnicos, O.S. por tipo/situação, agendamento) ainda não têm contrato
// confirmado para esta instalação — ver AGENTS.md §6 e
// docs/integracao-mk.md antes de adicionar qualquer chamada nova aqui. Não
// presumir o formato de resposta de um endpoint ainda não testado.
package mk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// maxResponseBytes limita a leitura da resposta do MK; nunca deveria passar
// disso (confirmado nos clientes de referência).
const maxResponseBytes = 2 << 20

// sys é sempre "MK0" nos dois sistemas MK reais já integrados pelo usuário.
// Não é configurável: nunca foi observado outro valor.
const sys = "MK0"

// defaultTokenTTL: o MK não documenta a expiração real do token temporário;
// os dois clientes de referência usam 5 minutos como padrão prático.
const defaultTokenTTL = 5 * time.Minute

// AuthError identifica uma falha ao autenticar ou consumir o MK, sem nunca
// incluir a URL da requisição na mensagem — um *url.Error nativo do Go
// embute a URL completa (com token e senha na query string) e um timeout
// real já vazou essas credenciais em log de produção (ver api-mk-octadesk).
type AuthError struct {
	Op  string
	Err error
}

func (e *AuthError) Error() string { return fmt.Sprintf("%s ao MK: %v", e.Op, sanitize(e.Err)) }
func (e *AuthError) Unwrap() error { return e.Err }

// sanitize remove qualquer *url.Error (e a URL que ele embute) da cadeia de
// erro, preservando só a causa (timeout, conexão recusada etc.).
func sanitize(err error) error {
	var urlErr *url.Error
	if errors.As(err, &urlErr) {
		return urlErr.Err
	}
	return err
}

// Client fala com a API .rule do MK. baseURL nunca deve ser logado com query
// string — use apenas o host/esquema em mensagens de erro.
type Client struct {
	baseURL    *url.URL
	inputToken string
	password   string
	http       *http.Client

	mu     sync.Mutex
	tokens map[string]cachedToken // chave: cd_servico — perfis podem ter permissões distintas por serviço.
}

type cachedToken struct {
	value     string
	expiresAt time.Time
}

// New cria o cliente MK. ruleBaseURL é a base da API .rule (ex.:
// https://sac.example.com.br); inputToken e password são as credenciais
// fixas do perfil de Webservice (nunca confundir token de entrada, fixo, com
// o token temporário retornado pela autenticação).
func New(ruleBaseURL, inputToken, password string, httpClient *http.Client) (*Client, error) {
	if ruleBaseURL == "" || inputToken == "" || password == "" {
		return nil, errors.New("configuração MK incompleta")
	}
	parsed, err := url.Parse(ruleBaseURL)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return nil, errors.New("MK_RULE_BASE_URL deve ser uma URL absoluta válida")
	}
	if httpClient == nil {
		return nil, errors.New("cliente HTTP é obrigatório")
	}
	return &Client{baseURL: parsed, inputToken: inputToken, password: password, http: httpClient, tokens: make(map[string]cachedToken)}, nil
}

// Token devolve um token temporário válido para o serviceCode informado,
// reautenticando quando necessário. serviceCode é o cd_servico do perfil de
// Webservice associado à operação que será chamada a seguir — perfis do MK
// podem autorizar serviços diferentes por código (AGENTS.md §6.3: cd_servico
// errado ou não habilitado falha na autenticação, não concede acesso a mais
// nada por si só).
func (c *Client) Token(ctx context.Context, serviceCode string) (string, error) {
	c.mu.Lock()
	if cached, ok := c.tokens[serviceCode]; ok && time.Now().Before(cached.expiresAt) {
		c.mu.Unlock()
		return cached.value, nil
	}
	c.mu.Unlock()

	token, err := c.authenticate(ctx, serviceCode)
	if err != nil {
		return "", err
	}

	c.mu.Lock()
	c.tokens[serviceCode] = cachedToken{value: token, expiresAt: time.Now().Add(defaultTokenTTL)}
	c.mu.Unlock()
	return token, nil
}

// InvalidateToken descarta o token em cache para serviceCode, forçando nova
// autenticação na próxima chamada — usar após um 401/403 do MK.
func (c *Client) InvalidateToken(serviceCode string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.tokens, serviceCode)
}

func (c *Client) authenticate(ctx context.Context, serviceCode string) (string, error) {
	query := url.Values{}
	query.Set("sys", sys)
	query.Set("token", c.inputToken)
	query.Set("password", c.password)
	query.Set("cd_servico", serviceCode)

	var payload any
	if err := c.get(ctx, "/mk/WSAutenticacao.rule", query, &payload); err != nil {
		return "", &AuthError{Op: "autenticar", Err: err}
	}

	token := findToken(payload)
	if token == "" {
		return "", &AuthError{Op: "autenticar", Err: errors.New("resposta não contém token de autenticação")}
	}
	return token, nil
}

// findToken busca recursivamente, ignorando maiúsculas/minúsculas e
// separadores, uma chave como "token", "tokenautenticacao" ou
// "tokenretornoautenticacao". Necessário porque o MK não é consistente no
// nome nem no nível de aninhamento desse campo entre instalações (confirmado
// em dois sistemas reais distintos — ver mk-consulta-cliente).
func findToken(payload any) string {
	object, ok := payload.(map[string]any)
	if !ok {
		return ""
	}
	wanted := []string{"tokenretornoautenticacao", "tokenautenticacao", "token"}
	for _, want := range wanted {
		for key, value := range object {
			normalized := strings.NewReplacer("_", "", "-", "", " ", "").Replace(strings.ToLower(key))
			if normalized == want {
				if text, ok := value.(string); ok && strings.TrimSpace(text) != "" {
					return strings.TrimSpace(text)
				}
			}
		}
	}
	for _, value := range object {
		if token := findToken(value); token != "" {
			return token
		}
	}
	return ""
}

// httpStatusError permite que chamadas de alto nível decidam reautenticar
// (401/403) sem depender de comparação de texto.
type httpStatusError struct{ code int }

func (e *httpStatusError) Error() string { return fmt.Sprintf("MK retornou HTTP %d", e.code) }

// get executa uma requisição GET autenticada contra o MK e decodifica o
// corpo em target. path deve começar com "/". Nunca inclui a URL montada em
// nenhum erro devolvido (ver AuthError/sanitize).
func (c *Client) get(ctx context.Context, path string, query url.Values, target any) error {
	reqURL := c.baseURL.ResolveReference(&url.URL{Path: path, RawQuery: query.Encode()})

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL.String(), nil)
	if err != nil {
		return errors.New("montar requisição ao MK")
	}
	request.Header.Set("Accept", "application/json")

	response, err := c.http.Do(request)
	if err != nil {
		return sanitize(err)
	}
	defer response.Body.Close()

	body, err := io.ReadAll(io.LimitReader(response.Body, maxResponseBytes))
	if err != nil {
		return errors.New("ler resposta do MK")
	}
	if response.StatusCode < 200 || response.StatusCode >= 300 {
		return &httpStatusError{code: response.StatusCode}
	}
	if err := json.Unmarshal(body, target); err != nil {
		return fmt.Errorf("decodificar resposta do MK: %w", err)
	}
	return nil
}

// ServiceType é um tipo de O.S. do catálogo do MK. Persistir Code; não usar
// Description para decidir elegibilidade a cada ciclo (AGENTS.md §6.4).
type ServiceType struct {
	Code        int64
	Description string
}

// ListServiceTypes chama WSMKOSListaTiposOS.rule e devolve o catálogo
// completo de tipos de O.S. cadastrados no MK. serviceCode é o cd_servico
// usado para autenticar antes desta chamada; reautentica uma vez e repete a
// chamada se o MK recusar o token com 401/403.
func (c *Client) ListServiceTypes(ctx context.Context, serviceCode string) ([]ServiceType, error) {
	for attempt := 0; attempt < 2; attempt++ {
		token, err := c.Token(ctx, serviceCode)
		if err != nil {
			return nil, err
		}

		types, err := c.listServiceTypesWithToken(ctx, token)
		if err == nil {
			return types, nil
		}
		var statusErr *httpStatusError
		if attempt == 0 && errors.As(err, &statusErr) && (statusErr.code == http.StatusUnauthorized || statusErr.code == http.StatusForbidden) {
			c.InvalidateToken(serviceCode)
			continue
		}
		return nil, err
	}
	return nil, errors.New("não foi possível listar os tipos de O.S. no MK")
}

func (c *Client) listServiceTypesWithToken(ctx context.Context, token string) ([]ServiceType, error) {
	query := url.Values{}
	query.Set("sys", sys)
	query.Set("token", token)

	var parsed struct {
		Status string `json:"status"`
		Tipos  []struct {
			Code        int64  `json:"codostipo"`
			Description string `json:"descricao"`
		} `json:"Tipos"`
	}
	if err := c.get(ctx, "/mk/WSMKOSListaTiposOS.rule", query, &parsed); err != nil {
		return nil, err
	}
	if !strings.EqualFold(parsed.Status, "OK") {
		return nil, fmt.Errorf("MK retornou status %q em WSMKOSListaTiposOS", parsed.Status)
	}

	types := make([]ServiceType, 0, len(parsed.Tipos))
	for _, tipo := range parsed.Tipos {
		types = append(types, ServiceType{Code: tipo.Code, Description: tipo.Description})
	}
	return types, nil
}
