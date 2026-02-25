package auth

import (
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

func TestJWTValidator_FetchKeycloakPublicKey(t *testing.T) {
	// Generate a test RSA key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	// Create a mock Keycloak JWKS endpoint
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/realms/test-realm/protocol/openid-connect/certs" {
			http.NotFound(w, r)
			return
		}

		// Encode public key as JWKS
		n := base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x00, 0x01}) // 65537

		jwks := map[string]interface{}{
			"keys": []map[string]interface{}{
				{
					"kty": "RSA",
					"use": "sig",
					"kid": "test-key-id",
					"n":   n,
					"e":   e,
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jwks)
	}))
	defer server.Close()

	validator := &JWTValidator{
		keycloakURL: server.URL,
		realm:       "test-realm",
	}

	pubKey, err := validator.fetchKeycloakPublicKey()
	if err != nil {
		t.Fatalf("failed to fetch public key: %v", err)
	}

	if pubKey == nil {
		t.Fatal("expected public key, got nil")
	}
}

func TestJWTValidator_ValidateToken(t *testing.T) {
	// Generate a test RSA key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	// Create a mock Keycloak JWKS endpoint
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x00, 0x01})

		jwks := map[string]interface{}{
			"keys": []map[string]interface{}{
				{
					"kty": "RSA",
					"use": "sig",
					"kid": "test-key-id",
					"n":   n,
					"e":   e,
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jwks)
	}))
	defer server.Close()

	validator := NewJWTValidator(server.URL, "test-realm")

	tests := []struct {
		name      string
		claims    jwt.MapClaims
		expectErr bool
	}{
		{
			name: "valid token with groups",
			claims: jwt.MapClaims{
				"exp":    time.Now().Add(time.Hour).Unix(),
				"iat":    time.Now().Unix(),
				"sub":    "user-123",
				"groups": []interface{}{"admin", "users"},
			},
			expectErr: false,
		},
		{
			name: "expired token",
			claims: jwt.MapClaims{
				"exp":    time.Now().Add(-time.Hour).Unix(),
				"iat":    time.Now().Unix(),
				"sub":    "user-123",
				"groups": []interface{}{"admin"},
			},
			expectErr: true,
		},
		{
			name: "token without groups claim",
			claims: jwt.MapClaims{
				"exp": time.Now().Add(time.Hour).Unix(),
				"iat": time.Now().Unix(),
				"sub": "user-123",
			},
			expectErr: false, // Should be valid, just empty groups
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			token := jwt.NewWithClaims(jwt.SigningMethodRS256, tt.claims)
			token.Header["kid"] = "test-key-id"

			tokenString, err := token.SignedString(privateKey)
			if err != nil {
				t.Fatalf("failed to sign token: %v", err)
			}

			claims, err := validator.ValidateToken(tokenString)
			if tt.expectErr {
				if err == nil {
					t.Error("expected error, got nil")
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if claims == nil && !tt.expectErr {
					t.Error("expected claims, got nil")
				}
			}
		})
	}
}

func TestJWTValidator_ExtractGroups(t *testing.T) {
	// Generate a test RSA key
	privateKey, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("failed to generate RSA key: %v", err)
	}

	// Create a mock Keycloak JWKS endpoint
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := base64.RawURLEncoding.EncodeToString(privateKey.PublicKey.N.Bytes())
		e := base64.RawURLEncoding.EncodeToString([]byte{0x01, 0x00, 0x01})

		jwks := map[string]interface{}{
			"keys": []map[string]interface{}{
				{
					"kty": "RSA",
					"use": "sig",
					"kid": "test-key-id",
					"n":   n,
					"e":   e,
				},
			},
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(jwks)
	}))
	defer server.Close()

	validator := NewJWTValidator(server.URL, "test-realm")

	tests := []struct {
		name           string
		groups         []interface{}
		expectedGroups []string
	}{
		{
			name:           "groups as strings",
			groups:         []interface{}{"admin", "users", "developers"},
			expectedGroups: []string{"admin", "users", "developers"},
		},
		{
			name:           "empty groups",
			groups:         []interface{}{},
			expectedGroups: []string{},
		},
		{
			name:           "mixed types (should stringify)",
			groups:         []interface{}{"admin", 123, "users"},
			expectedGroups: []string{"admin", "123", "users"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims := jwt.MapClaims{
				"exp":    time.Now().Add(time.Hour).Unix(),
				"iat":    time.Now().Unix(),
				"sub":    "user-123",
				"groups": tt.groups,
			}

			token := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
			token.Header["kid"] = "test-key-id"

			tokenString, err := token.SignedString(privateKey)
			if err != nil {
				t.Fatalf("failed to sign token: %v", err)
			}

			validatedClaims, err := validator.ValidateToken(tokenString)
			if err != nil {
				t.Fatalf("failed to validate token: %v", err)
			}

			extractedGroups := extractGroups(validatedClaims)
			if len(extractedGroups) != len(tt.expectedGroups) {
				t.Errorf("expected %d groups, got %d", len(tt.expectedGroups), len(extractedGroups))
			}

			for i, expected := range tt.expectedGroups {
				if extractedGroups[i] != expected {
					t.Errorf("group[%d]: expected %s, got %s", i, expected, extractedGroups[i])
				}
			}
		})
	}
}

// Helper function for testing group extraction
func extractGroups(claims *jwt.MapClaims) []string {
	if claims == nil {
		return []string{}
	}

	groupsInterface, ok := (*claims)["groups"]
	if !ok {
		return []string{}
	}

	groupsSlice, ok := groupsInterface.([]interface{})
	if !ok {
		return []string{}
	}

	groups := make([]string, 0, len(groupsSlice))
	for _, g := range groupsSlice {
		groups = append(groups, fmt.Sprintf("%v", g))
	}

	return groups
}
