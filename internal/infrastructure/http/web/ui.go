package web

import (
	"context"
	"embed"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/go-chi/chi/v5"
)

//go:embed static
var staticFS embed.FS

type TokenCache struct {
	mu        sync.RWMutex
	tokens    map[string]string
	expiresAt time.Time
}

var cache = &TokenCache{
	tokens: make(map[string]string),
}

// TokensResponse é o formato retornado para o frontend.
type TokensResponse struct {
	Internal  string `json:"internal"`
	ProviderA string `json:"providerA"`
	ProviderB string `json:"providerB"`
}

// RegisterWebRoutes registra as rotas da interface visual retro e endpoints auxiliares.
func RegisterWebRoutes(r chi.Router, authIssuer string, logger *slog.Logger) {
	// Subdiretório "static" embutido
	subFS, err := fs.Sub(staticFS, "static")
	if err != nil {
		logger.Error("Falha ao criar sub-filesystem estático para web UI", "error", err)
		return
	}

	fileServer := http.FileServer(http.FS(subFS))

	redirectRoot := func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/app/", http.StatusTemporaryRedirect)
	}
	r.Get("/", redirectRoot)
	r.Head("/", redirectRoot)

	redirectApp := func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/app/", http.StatusMovedPermanently)
	}
	r.Get("/app", redirectApp)
	r.Head("/app", redirectApp)

	// Favicon root redirect
	r.Get("/favicon.ico", func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "/app/favicon.png", http.StatusMovedPermanently)
	})

	// Endpoint para obter tokens JWT do Keycloak
	r.Get("/app/api/tokens", func(w http.ResponseWriter, r *http.Request) {
		tokens, err := getTokens(r.Context(), authIssuer, logger)
		if err != nil {
			logger.Error("Falha ao obter tokens para web UI", "error", err)
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusServiceUnavailable)
			_ = json.NewEncoder(w).Encode(map[string]string{
				"error":   "Falha ao comunicar com o Keycloak: " + err.Error(),
				"details": "Verifique se o Keycloak está rodando na porta 8080",
			})
			return
		}

		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(tokens)
	})

	// Servir os assets estáticos sob /app/* com desativação explícita de cache
	staticHandler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache, no-store, must-revalidate")
		w.Header().Set("Pragma", "no-cache")
		w.Header().Set("Expires", "0")

		if strings.HasSuffix(r.URL.Path, ".wasm") {
			w.Header().Set("Content-Type", "application/wasm")
		}

		rctx := chi.RouteContext(r.Context())
		pathPrefix := strings.TrimSuffix(rctx.RoutePattern(), "/*")
		fsHandler := http.StripPrefix(pathPrefix, fileServer)
		fsHandler.ServeHTTP(w, r)
	}
	r.Get("/app/*", staticHandler)
	r.Head("/app/*", staticHandler)
}

// getTokens busca os tokens das 3 credenciais (internal, provider-a, provider-b) com cache de 45 minutos.
func getTokens(ctx context.Context, authIssuer string, logger *slog.Logger) (*TokensResponse, error) {
	cache.mu.RLock()
	if time.Now().Before(cache.expiresAt) && len(cache.tokens) >= 3 {
		resp := &TokensResponse{
			Internal:  cache.tokens["internal-service"],
			ProviderA: cache.tokens["provider-a"],
			ProviderB: cache.tokens["provider-b"],
		}
		cache.mu.RUnlock()
		return resp, nil
	}
	cache.mu.RUnlock()

	cache.mu.Lock()
	defer cache.mu.Unlock()

	// Checagem dupla após adquirir lock de escrita
	if time.Now().Before(cache.expiresAt) && len(cache.tokens) >= 3 {
		return &TokensResponse{
			Internal:  cache.tokens["internal-service"],
			ProviderA: cache.tokens["provider-a"],
			ProviderB: cache.tokens["provider-b"],
		}, nil
	}

	clients := []struct {
		id     string
		secret string
	}{
		{"internal-service", "secret-internal"},
		{"provider-a", "secret-a"},
		{"provider-b", "secret-b"},
	}

	newTokens := make(map[string]string)
	for _, c := range clients {
		token, err := fetchKeycloakToken(ctx, authIssuer, c.id, c.secret, logger)
		if err != nil {
			return nil, err
		}
		newTokens[c.id] = token
	}

	cache.tokens = newTokens
	cache.expiresAt = time.Now().Add(45 * time.Minute)

	return &TokensResponse{
		Internal:  cache.tokens["internal-service"],
		ProviderA: cache.tokens["provider-a"],
		ProviderB: cache.tokens["provider-b"],
	}, nil
}

func fetchKeycloakToken(ctx context.Context, authIssuer, clientID, clientSecret string, logger *slog.Logger) (string, error) {
	candidates := []string{
		authIssuer + "/protocol/openid-connect/token",
		"http://keycloak:8080/realms/betting/protocol/openid-connect/token",
		"http://localhost:8080/realms/betting/protocol/openid-connect/token",
	}

	var lastErr error
	client := &http.Client{Timeout: 5 * time.Second}

	for _, tokenURL := range candidates {
		data := url.Values{}
		data.Set("grant_type", "client_credentials")
		data.Set("client_id", clientID)
		data.Set("client_secret", clientSecret)

		req, err := http.NewRequestWithContext(ctx, http.MethodPost, tokenURL, strings.NewReader(data.Encode()))
		if err != nil {
			logger.DebugContext(ctx, "Falha ao criar requisição de token", "url", tokenURL, "error", err)
			lastErr = err
			continue
		}
		req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		// Envia Host header caso o Keycloak espere o hostname do container para gerar o iss correto
		req.Header.Set("Host", "keycloak:8080")

		resp, err := client.Do(req)
		if err != nil {
			logger.DebugContext(ctx, "Falha ao conectar ao endpoint do Keycloak", "url", tokenURL, "error", err)
			lastErr = err
			continue
		}

		if resp.StatusCode != http.StatusOK {
			logger.WarnContext(ctx, "Keycloak retornou status HTTP inesperado", "url", tokenURL, "status", resp.StatusCode)
			resp.Body.Close()
			lastErr = err
			continue
		}

		var payload struct {
			AccessToken string `json:"access_token"`
		}
		decodeErr := json.NewDecoder(resp.Body).Decode(&payload)
		resp.Body.Close()
		if decodeErr != nil {
			logger.WarnContext(ctx, "Falha ao decodificar resposta JSON do Keycloak", "url", tokenURL, "error", decodeErr)
			lastErr = decodeErr
			continue
		}

		if payload.AccessToken != "" {
			return payload.AccessToken, nil
		}
	}

	return "", lastErr
}
