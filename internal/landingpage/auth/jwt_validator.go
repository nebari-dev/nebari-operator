package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	ctrl "sigs.k8s.io/controller-runtime"
)

var log = ctrl.Log.WithName("jwt-validator")

// Claims represents the JWT claims we care about
type Claims struct {
	jwt.RegisteredClaims
	Email             string   `json:"email"`
	Name              string   `json:"name"`
	PreferredUsername string   `json:"preferred_username"`
	Groups            []string `json:"groups"`
}

// JWTValidator validates JWT tokens from Keycloak
type JWTValidator struct {
	keycloakURL string
	realm       string
	publicKeys  map[string]*rsa.PublicKey
	keysMu      sync.RWMutex
	lastFetch   time.Time
}

// JWK represents a JSON Web Key
type JWK struct {
	Kty string `json:"kty"`
	Kid string `json:"kid"`
	Use string `json:"use"`
	N   string `json:"n"`
	E   string `json:"e"`
}

// JWKS represents a JSON Web Key Set
type JWKS struct {
	Keys []JWK `json:"keys"`
}

// NewJWTValidator creates a new JWT validator
func NewJWTValidator(keycloakURL, realm string) (*JWTValidator, error) {
	v := &JWTValidator{
		keycloakURL: strings.TrimSuffix(keycloakURL, "/"),
		realm:       realm,
		publicKeys:  make(map[string]*rsa.PublicKey),
	}

	if err := v.fetchPublicKeys(); err != nil {
		return nil, fmt.Errorf("failed to fetch public keys: %w", err)
	}

	log.Info("JWT validator initialized", "keycloakURL", keycloakURL, "realm", realm)
	return v, nil
}

// ValidateToken validates a JWT token and returns the claims
func (v *JWTValidator) ValidateToken(tokenString string) (*Claims, error) {
	if time.Since(v.lastFetch) > time.Hour {
		if err := v.fetchPublicKeys(); err != nil {
			log.Error(err, "Failed to refresh public keys")
		}
	}

	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(token *jwt.Token) (interface{}, error) {
		if _, ok := token.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("unexpected signing method: %v", token.Header["alg"])
		}

		kid, ok := token.Header["kid"].(string)
		if !ok {
			return nil, fmt.Errorf("missing kid in token header")
		}

		v.keysMu.RLock()
		defer v.keysMu.RUnlock()

		publicKey, exists := v.publicKeys[kid]
		if !exists {
			return nil, fmt.Errorf("unknown key ID: %s", kid)
		}

		return publicKey, nil
	})

	if err != nil {
		return nil, fmt.Errorf("token validation failed: %w", err)
	}

	if !token.Valid {
		return nil, fmt.Errorf("invalid token")
	}

	expectedIssuer := fmt.Sprintf("%s/realms/%s", v.keycloakURL, v.realm)
	if claims.Issuer != expectedIssuer {
		return nil, fmt.Errorf("invalid issuer: expected %s, got %s", expectedIssuer, claims.Issuer)
	}

	if time.Now().After(claims.ExpiresAt.Time) {
		return nil, fmt.Errorf("token expired")
	}

	return claims, nil
}

func (v *JWTValidator) fetchPublicKeys() error {
	certsURL := fmt.Sprintf("%s/realms/%s/protocol/openid-connect/certs", v.keycloakURL, v.realm)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	req, err := http.NewRequestWithContext(ctx, "GET", certsURL, nil)
	if err != nil {
		return fmt.Errorf("failed to create request: %w", err)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("failed to fetch keys: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("failed to fetch keys: status %d", resp.StatusCode)
	}

	var jwks JWKS
	if err := json.NewDecoder(resp.Body).Decode(&jwks); err != nil {
		return fmt.Errorf("failed to decode JWKS: %w", err)
	}

	v.keysMu.Lock()
	defer v.keysMu.Unlock()

	v.publicKeys = make(map[string]*rsa.PublicKey)

	for _, jwk := range jwks.Keys {
		if jwk.Kty != "RSA" {
			continue
		}

		publicKey, err := parseRSAPublicKey(jwk)
		if err != nil {
			log.Error(err, "Failed to parse RSA public key", "kid", jwk.Kid)
			continue
		}

		v.publicKeys[jwk.Kid] = publicKey
		log.Info("Loaded public key", "kid", jwk.Kid)
	}

	v.lastFetch = time.Now()

	if len(v.publicKeys) == 0 {
		return fmt.Errorf("no valid RSA keys found")
	}

	log.Info("Public keys refreshed", "count", len(v.publicKeys))
	return nil
}

func parseRSAPublicKey(jwk JWK) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(jwk.N)
	if err != nil {
		return nil, fmt.Errorf("failed to decode N: %w", err)
	}

	eBytes, err := base64.RawURLEncoding.DecodeString(jwk.E)
	if err != nil {
		return nil, fmt.Errorf("failed to decode E: %w", err)
	}

	n := new(big.Int).SetBytes(nBytes)
	e := 0
	for _, b := range eBytes {
		e = e*256 + int(b)
	}

	return &rsa.PublicKey{
		N: n,
		E: e,
	}, nil
}
