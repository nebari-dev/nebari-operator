package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	landingcache "github.com/nebari-dev/nebari-operator/internal/landingpage/cache"
)

func TestGetServicesHandler_Public(t *testing.T) {
	cache := landingcache.NewServiceCache()

	// Add test services
	cache.Add(&landingcache.ServiceInfo{
		Name:       "public-service",
		Namespace:  "default",
		URL:        "https://public.example.com",
		Visibility: "public",
		Title:      "Public Service",
		Category:   "general",
	})
	cache.Add(&landingcache.ServiceInfo{
		Name:       "authenticated-service",
		Namespace:  "default",
		URL:        "https://auth.example.com",
		Visibility: "authenticated",
		Title:      "Auth Service",
	})

	handler := NewGetServicesHandler(cache, nil) // No JWT validator for public access

	req := httptest.NewRequest("GET", "/services", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", w.Code)
	}

	var response ServiceListResponse
	if err := json.NewDecoder(w.Body).Decode(&response); err != nil {
		t.Fatalf("failed to decode response: %v", err)
	}

	// Unauthenticated request should only see public services
	if len(response.Services) != 1 {
		t.Errorf("expected 1 service, got %d", len(response.Services))
	}

	if response.Services[0].Name != "public-service" {
		t.Errorf("expected public-service, got %s", response.Services[0].Name)
	}
}

func TestGetServicesHandler_CORS(t *testing.T) {
	cache := landingcache.NewServiceCache()
	handler := NewGetServicesHandler(cache, nil)

	req := httptest.NewRequest("OPTIONS", "/services", nil)
	req.Header.Set("Origin", "https://frontend.example.com")
	w := httptest.NewRecorder()

	handler(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("expected status 200 for OPTIONS, got %d", w.Code)
	}

	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("expected CORS header to be set")
	}

	if w.Header().Get("Access-Control-Allow-Methods") == "" {
		t.Error("expected Access-Control-Allow-Methods header")
	}
}

func TestGetServicesHandler_CategoryFilter(t *testing.T) {
	cache := landingcache.NewServiceCache()

	// Add services with different categories
	cache.Add(&landingcache.ServiceInfo{
		Name:       "ds-service",
		Namespace:  "default",
		Visibility: "public",
		Category:   "data-science",
	})
	cache.Add(&landingcache.ServiceInfo{
		Name:       "dev-service",
		Namespace:  "default",
		Visibility: "public",
		Category:   "development",
	})

	handler := NewGetServicesHandler(cache, nil)

	req := httptest.NewRequest("GET", "/services?category=data-science", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	var response ServiceListResponse
	json.NewDecoder(w.Body).Decode(&response)

	if len(response.Services) != 1 {
		t.Errorf("expected 1 service with category filter, got %d", len(response.Services))
	}

	if response.Services[0].Category != "data-science" {
		t.Errorf("expected data-science category, got %s", response.Services[0].Category)
	}
}

func TestGetServicesHandler_Sorting(t *testing.T) {
	cache := landingcache.NewServiceCache()

	priority1 := 10
	priority2 := 5
	priority3 := 15

	// Add services with different priorities
	cache.Add(&landingcache.ServiceInfo{
		Name:       "service-b",
		Namespace:  "default",
		Visibility: "public",
		Priority:   &priority1,
		Title:      "Service B",
	})
	cache.Add(&landingcache.ServiceInfo{
		Name:       "service-a",
		Namespace:  "default",
		Visibility: "public",
		Priority:   &priority2,
		Title:      "Service A",
	})
	cache.Add(&landingcache.ServiceInfo{
		Name:       "service-c",
		Namespace:  "default",
		Visibility: "public",
		Priority:   &priority3,
		Title:      "Service C",
	})

	handler := NewGetServicesHandler(cache, nil)

	req := httptest.NewRequest("GET", "/services", nil)
	w := httptest.NewRecorder()

	handler(w, req)

	var response ServiceListResponse
	json.NewDecoder(w.Body).Decode(&response)

	// Services should be sorted by priority (descending)
	if len(response.Services) != 3 {
		t.Errorf("expected 3 services, got %d", len(response.Services))
	}

	// First should be service-c (priority 15)
	if response.Services[0].Name != "service-c" {
		t.Errorf("expected service-c first, got %s", response.Services[0].Name)
	}

	// Last should be service-a (priority 5)
	if response.Services[2].Name != "service-a" {
		t.Errorf("expected service-a last, got %s", response.Services[2].Name)
	}
}

// Mock response types for testing
type ServiceListResponse struct {
	Services []*landingcache.ServiceInfo `json:"services"`
}
