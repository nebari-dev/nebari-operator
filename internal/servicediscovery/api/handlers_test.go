// Copyright 2026, OpenTeams.
// SPDX-License-Identifier: Apache-2.0

package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	sdapp "github.com/nebari-dev/nebari-operator/internal/servicediscovery/app"
	"github.com/nebari-dev/nebari-operator/internal/servicediscovery/cache"
)

// --- helpers ---

func buildCache(entries ...struct {
	uid, name, visibility, category string
	priority                        int
}) *cache.ServiceCache {
	sc := cache.NewServiceCache()
	for _, e := range entries {
		sc.Add(&sdapp.App{
			UID:        e.uid,
			Name:       e.name,
			Namespace:  "default",
			Hostname:   e.name + ".example.com",
			TLSEnabled: true,
			LandingPage: &sdapp.LandingPage{
				Enabled:     true,
				DisplayName: e.name,
				Category:    e.category,
				Priority:    e.priority,
				Visibility:  e.visibility,
			},
		})
	}
	return sc
}

// newHandler returns a Handler with auth disabled.
func newTestHandler(sc *cache.ServiceCache) *Handler {
	return NewHandler(sc, nil, false, nil, nil)
}

// newAuthHandler returns a Handler with auth enabled but no validator.
func newAuthTestHandler(sc *cache.ServiceCache) *Handler {
	return NewHandler(sc, nil, true, nil, nil)
}

type entry = struct {
	uid, name, visibility, category string
	priority                        int
}

func doGet(t *testing.T, h http.Handler, path string, headers ...string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodGet, path, nil)
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	return rr
}

// --- /api/v1/health ---

func TestHandleHealth_ReturnsOK(t *testing.T) {
	rr := doGet(t, newTestHandler(cache.NewServiceCache()).Routes(), "/api/v1/health")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "healthy" {
		t.Errorf(`expected "healthy", got %v`, body)
	}
}

func TestHandleHealth_ContentTypeJSON(t *testing.T) {
	rr := doGet(t, newTestHandler(cache.NewServiceCache()).Routes(), "/api/v1/health")
	if ct := rr.Header().Get("Content-Type"); ct != "application/json" {
		t.Errorf("expected application/json, got %q", ct)
	}
}

// --- /api/v1/categories ---

func TestHandleGetCategories_ReturnsSortedUnique(t *testing.T) {
	sc := buildCache(
		entry{"u1", "a", "public", "Monitoring", 0},
		entry{"u2", "b", "public", "Development", 0},
		entry{"u3", "c", "public", "Monitoring", 0},
	)
	rr := doGet(t, newTestHandler(sc).Routes(), "/api/v1/categories")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var body map[string][]string
	if err := json.NewDecoder(rr.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	cats := body["categories"]
	want := []string{"Development", "Monitoring"}
	if len(cats) != len(want) {
		t.Fatalf("expected %v, got %v", want, cats)
	}
	for i, c := range cats {
		if c != want[i] {
			t.Errorf("pos %d: expected %q, got %q", i, want[i], c)
		}
	}
}

func TestHandleGetCategories_MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/categories", nil)
	rr := httptest.NewRecorder()
	newTestHandler(cache.NewServiceCache()).Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

// --- /api/v1/services ---

func TestHandleGetServices_MethodNotAllowed(t *testing.T) {
	req := httptest.NewRequest(http.MethodPost, "/api/v1/services", nil)
	rr := httptest.NewRecorder()
	newTestHandler(cache.NewServiceCache()).Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestHandleGetServices_AuthDisabled_OnlyPublicVisible(t *testing.T) {
	sc := buildCache(
		entry{"u1", "pub", "public", "", 0},
		entry{"u2", "auth", "authenticated", "", 0},
		entry{"u3", "priv", "private", "", 0},
	)
	rr := doGet(t, newTestHandler(sc).Routes(), "/api/v1/services")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp ServiceResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Services.Public) != 1 || resp.Services.Public[0].Name != "pub" {
		t.Errorf("expected 1 public service, got %v", resp.Services.Public)
	}
	if len(resp.Services.Authenticated) != 0 {
		t.Errorf("expected 0 authenticated services, got %v", resp.Services.Authenticated)
	}
	if len(resp.Services.Private) != 0 {
		t.Errorf("expected 0 private services, got %v", resp.Services.Private)
	}
	if resp.User != nil {
		t.Errorf("expected no user info, got %v", resp.User)
	}
}

func TestHandleGetServices_AuthEnabledNoToken_OnlyPublicVisible(t *testing.T) {
	sc := buildCache(
		entry{"u1", "pub", "public", "", 0},
		entry{"u2", "auth", "authenticated", "", 0},
	)
	rr := doGet(t, newAuthTestHandler(sc).Routes(), "/api/v1/services")
	if rr.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d", rr.Code)
	}
	var resp ServiceResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Services.Public) != 1 {
		t.Errorf("expected 1 public service, got %d", len(resp.Services.Public))
	}
	if len(resp.Services.Authenticated) != 0 {
		t.Errorf("expected 0 authenticated services without token, got %d", len(resp.Services.Authenticated))
	}
}

func TestHandleGetServices_DefaultVisibility_TreatedAsAuthenticated(t *testing.T) {
	sc := cache.NewServiceCache()
	sc.Add(&sdapp.App{
		UID:        "u-def",
		Name:       "default-vis",
		Namespace:  "ns",
		Hostname:   "h.com",
		TLSEnabled: true,
		LandingPage: &sdapp.LandingPage{
			Enabled:    true,
			Visibility: "", // no Visibility → defaults to "authenticated"
		},
	})
	rr := doGet(t, newTestHandler(sc).Routes(), "/api/v1/services")
	var resp ServiceResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Services.Authenticated) != 0 {
		t.Errorf("expected 0 authenticated services without token, got %d", len(resp.Services.Authenticated))
	}
}

func TestHandleGetServices_ResponseContainsCategories(t *testing.T) {
	sc := buildCache(
		entry{"u1", "a", "public", "Tools", 0},
		entry{"u2", "b", "public", "Data", 0},
	)
	rr := doGet(t, newTestHandler(sc).Routes(), "/api/v1/services")
	var resp ServiceResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Categories) != 2 {
		t.Errorf("expected 2 categories, got %v", resp.Categories)
	}
}

// --- /api/v1/services/{namespace}/{name} ---

func TestHandleGetService_NotFound(t *testing.T) {
	rr := doGet(t, newTestHandler(cache.NewServiceCache()).Routes(), "/api/v1/services/default/does-not-exist")
	if rr.Code != http.StatusNotFound {
		t.Errorf("expected 404, got %d", rr.Code)
	}
}

func TestHandleGetService_PublicAccess_NoAuth(t *testing.T) {
	sc := buildCache(entry{"uid-pub", "pub", "public", "", 0})
	rr := doGet(t, newTestHandler(sc).Routes(), "/api/v1/services/default/pub")
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200, got %d", rr.Code)
	}
	var svc cache.ServiceInfo
	if err := json.NewDecoder(rr.Body).Decode(&svc); err != nil {
		t.Fatal(err)
	}
	if svc.Name != "pub" {
		t.Errorf("expected name 'pub', got %q", svc.Name)
	}
}

func TestHandleGetService_AuthenticatedService_Forbidden_NoAuth(t *testing.T) {
	sc := buildCache(entry{"uid-auth", "auth-svc", "authenticated", "", 0})
	rr := doGet(t, newTestHandler(sc).Routes(), "/api/v1/services/default/auth-svc")
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}

func TestHandleGetService_PrivateService_Forbidden_NoAuth(t *testing.T) {
	sc := buildCache(entry{"uid-priv", "priv-svc", "private", "", 0})
	rr := doGet(t, newTestHandler(sc).Routes(), "/api/v1/services/default/priv-svc")
	if rr.Code != http.StatusForbidden {
		t.Errorf("expected 403, got %d", rr.Code)
	}
}

func TestHandleGetService_MethodNotAllowed(t *testing.T) {
	sc := buildCache(entry{"u1", "pub", "public", "", 0})
	req := httptest.NewRequest(http.MethodPost, "/api/v1/services/default/pub", nil)
	rr := httptest.NewRecorder()
	newTestHandler(sc).Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", rr.Code)
	}
}

func TestHandleGetService_MissingNameOrNamespace_BadRequest(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/api/v1/services/only-one-segment", nil)
	rr := httptest.NewRecorder()
	newTestHandler(cache.NewServiceCache()).Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", rr.Code)
	}
}

// --- CORS ---

func TestCORSHeaders_PresentOnAllRequests(t *testing.T) {
	rr := doGet(t, newTestHandler(cache.NewServiceCache()).Routes(), "/api/v1/health")
	if rr.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("expected CORS origin header")
	}
}

func TestCORSHeaders_OptionsReturns200(t *testing.T) {
	req := httptest.NewRequest(http.MethodOptions, "/api/v1/services", nil)
	rr := httptest.NewRecorder()
	newTestHandler(cache.NewServiceCache()).Routes().ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Errorf("expected 200 for OPTIONS, got %d", rr.Code)
	}
}

// --- Auth header extraction ---

func TestHandleGetServices_InvalidAuthHeader_TreatedAsUnauthenticated(t *testing.T) {
	sc := buildCache(
		entry{"u1", "pub", "public", "", 0},
		entry{"u2", "auth", "authenticated", "", 0},
	)
	rr := doGet(t, newAuthTestHandler(sc).Routes(), "/api/v1/services",
		"Authorization", "NotBearer sometoken",
	)
	var resp ServiceResponse
	if err := json.NewDecoder(rr.Body).Decode(&resp); err != nil {
		t.Fatal(err)
	}
	if len(resp.Services.Authenticated) != 0 {
		t.Errorf("expected 0 authenticated services with bad auth header, got %d", len(resp.Services.Authenticated))
	}
}
