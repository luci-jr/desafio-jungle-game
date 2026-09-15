package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type contextKey string

const ProviderIDContextKey contextKey = "providerId"

var (
	ErrMissingToken    = errors.New("missing authorization bearer token")
	ErrInvalidToken    = errors.New("invalid or expired token")
	ErrForbiddenAccess = errors.New("forbidden: provider is not authorized for this resource")
)

type JWK struct {
	Kid string `json:"kid"`
	Kty string `json:"kty"`
	Alg string `json:"alg"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
}

type JWKSResponse struct {
	Keys []JWK `json:"keys"`
}

// TokenValidator valida tokens JWT assinados pelo Keycloak através de JWKS remoto.
type TokenValidator struct {
	jwksURL    string
	issuer     string
	keysCache  map[string]*rsa.PublicKey
	cacheMu    sync.RWMutex
	lastFetch  time.Time
	httpClient *http.Client
}

func NewTokenValidator(jwksURL, issuer string) *TokenValidator {
	return &TokenValidator{
		jwksURL:    jwksURL,
		issuer:     issuer,
		keysCache:  make(map[string]*rsa.PublicKey),
		httpClient: &http.Client{Timeout: 5 * time.Second},
	}
}

// ValidateToken valida o JWT e extrai a claim providerId.
func (v *TokenValidator) ValidateToken(tokenString string) (string, error) {
	tokenString = strings.TrimPrefix(tokenString, "Bearer ")
	tokenString = strings.TrimSpace(tokenString)
	if tokenString == "" {
		return "", ErrMissingToken
	}

	token, err := jwt.Parse(tokenString, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("método de assinatura inesperado: %v", t.Header["alg"])
		}

		kid, ok := t.Header["kid"].(string)
		if !ok {
			return nil, errors.New("header kid ausente no token")
		}

		key, err := v.getPublicKey(kid)
		if err != nil {
			return nil, err
		}
		return key, nil
	})

	if err != nil || !token.Valid {
		return "", ErrInvalidToken
	}

	claims, ok := token.Claims.(jwt.MapClaims)
	if !ok {
		return "", ErrInvalidToken
	}

	// Extrai providerId da claim personalizada
	if providerId, ok := claims["providerId"].(string); ok && providerId != "" {
		return providerId, nil
	}

	// Fallback para clientId ou azp caso configurado no Keycloak
	if azp, ok := claims["azp"].(string); ok && azp != "" {
		return azp, nil
	}

	return "", errors.New("claim providerId não encontrada no token")
}

func (v *TokenValidator) getPublicKey(kid string) (*rsa.PublicKey, error) {
	v.cacheMu.RLock()
	key, exists := v.keysCache[kid]
	recent := time.Since(v.lastFetch) < 10*time.Minute
	v.cacheMu.RUnlock()

	if exists && recent {
		return key, nil
	}

	v.cacheMu.Lock()
	defer v.cacheMu.Unlock()

	// Busca chaves atualizadas do Keycloak JWKS
	resp, err := v.httpClient.Get(v.jwksURL)
	if err != nil {
		if exists {
			return key, nil // Usa cache antigo se o Keycloak estiver temporariamente inacessível
		}
		return nil, fmt.Errorf("falha ao consultar JWKS do Keycloak: %w", err)
	}
	defer resp.Body.Close()

	var jwks JWKSResponse
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return nil, fmt.Errorf("falha ao decodificar JWKS: %w", err)
	}

	for _, k := range jwks.Keys {
		pubKey, err := parseRSAPublicKey(k.N, k.E)
		if err == nil {
			v.keysCache[k.Kid] = pubKey
		}
	}
	v.lastFetch = time.Now()

	key, found := v.keysCache[kid]
	if !found {
		return nil, fmt.Errorf("chave pública kid=%s não encontrada no JWKS", kid)
	}
	return key, nil
}

func parseRSAPublicKey(nStr, eStr string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(nStr, "="))
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimRight(eStr, "="))
	if err != nil {
		return nil, err
	}

	var eInt uint64
	for _, b := range eBytes {
		eInt = (eInt << 8) | uint64(b)
	}

	n := new(big.Int).SetBytes(nBytes)
	return &rsa.PublicKey{
		N: n,
		E: int(eInt),
	}, nil
}

// MiddlewareHTTP intercepta a requisição, valida o token e injeta o providerId no Context.
func (v *TokenValidator) MiddlewareHTTP(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Ignora rotas públicas de health check
		if strings.HasPrefix(r.URL.Path, "/health") {
			next.ServeHTTP(w, r)
			return
		}

		authHeader := r.Header.Get("Authorization")
		if authHeader == "" {
			http.Error(w, `{"error":"Authorization header missing"}`, http.StatusUnauthorized)
			return
		}

		providerId, err := v.ValidateToken(authHeader)
		if err != nil {
			http.Error(w, fmt.Sprintf(`{"error":"%s"}`, err.Error()), http.StatusUnauthorized)
			return
		}

		ctx := context.WithValue(r.Context(), ProviderIDContextKey, providerId)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

// GetProviderID extrai o providerId autenticado do contexto da requisição.
func GetProviderID(ctx context.Context) string {
	if val, ok := ctx.Value(ProviderIDContextKey).(string); ok {
		return val
	}
	return ""
}
