package cache

import (
	"testing"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestNewServiceCache(t *testing.T) {
	c := NewServiceCache()
	if c == nil {
		t.Fatal("NewServiceCache returned nil")
	}
	if len(c.GetAll()) != 0 {
		t.Errorf("expected empty cache, got %d services", len(c.GetAll()))
	}
}

func TestServiceCache_Add(t *testing.T) {
	c := NewServiceCache()

	service := &ServiceInfo{
		Name:       "test-service",
		Namespace:  "default",
		URL:        "https://test.example.com",
		Visibility: "public",
		Category:   "data-science",
	}

	c.Add(service)

	retrieved := c.Get("default", "test-service")
	if retrieved == nil {
		t.Fatal("failed to retrieve added service")
	}

	if retrieved.Name != service.Name || retrieved.URL != service.URL {
		t.Errorf("retrieved service doesn't match: got %+v, want %+v", retrieved, service)
	}
}

func TestServiceCache_Remove(t *testing.T) {
	c := NewServiceCache()

	service := &ServiceInfo{
		Name:      "test-service",
		Namespace: "default",
	}

	c.Add(service)
	c.Remove("default", "test-service")

	retrieved := c.Get("default", "test-service")
	if retrieved != nil {
		t.Errorf("service should have been removed, but got %+v", retrieved)
	}
}

func TestServiceCache_GetAll(t *testing.T) {
	c := NewServiceCache()

	services := []*ServiceInfo{
		{Name: "service1", Namespace: "default", Visibility: "public"},
		{Name: "service2", Namespace: "default", Visibility: "authenticated"},
		{Name: "service3", Namespace: "other", Visibility: "private", RequiredGroups: []string{"admin"}},
	}

	for _, s := range services {
		c.Add(s)
	}

	all := c.GetAll()
	if len(all) != len(services) {
		t.Errorf("expected %d services, got %d", len(services), len(all))
	}
}

func TestServiceCache_VisibilityFiltering(t *testing.T) {
	c := NewServiceCache()

	// Add services with different visibility levels
	c.Add(&ServiceInfo{Name: "public-svc", Namespace: "default", Visibility: "public"})
	c.Add(&ServiceInfo{Name: "auth-svc", Namespace: "default", Visibility: "authenticated"})
	c.Add(&ServiceInfo{Name: "private-svc", Namespace: "default", Visibility: "private", RequiredGroups: []string{"admin"}})

	tests := []struct {
		name          string
		userGroups    []string
		expectedCount int
		expectedNames []string
	}{
		{
			name:          "unauthenticated user sees only public",
			userGroups:    nil,
			expectedCount: 1,
			expectedNames: []string{"public-svc"},
		},
		{
			name:          "authenticated user without groups sees public and authenticated",
			userGroups:    []string{},
			expectedCount: 2,
			expectedNames: []string{"public-svc", "auth-svc"},
		},
		{
			name:          "admin user sees all services",
			userGroups:    []string{"admin"},
			expectedCount: 3,
			expectedNames: []string{"public-svc", "auth-svc", "private-svc"},
		},
		{
			name:          "non-admin user doesn't see private",
			userGroups:    []string{"users"},
			expectedCount: 2,
			expectedNames: []string{"public-svc", "auth-svc"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			filtered := filterByVisibility(c.GetAll(), tt.userGroups)
			if len(filtered) != tt.expectedCount {
				t.Errorf("expected %d services, got %d", tt.expectedCount, len(filtered))
			}

			for _, expectedName := range tt.expectedNames {
				found := false
				for _, svc := range filtered {
					if svc.Name == expectedName {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected to find service %s, but it was not in filtered results", expectedName)
				}
			}
		})
	}
}

// Helper function to test visibility filtering logic
func filterByVisibility(services []*ServiceInfo, userGroups []string) []*ServiceInfo {
	var filtered []*ServiceInfo
	isAuthenticated := userGroups != nil

	for _, svc := range services {
		switch svc.Visibility {
		case "public":
			filtered = append(filtered, svc)
		case "authenticated":
			if isAuthenticated {
				filtered = append(filtered, svc)
			}
		case "private":
			if isAuthenticated && hasRequiredGroups(userGroups, svc.RequiredGroups) {
				filtered = append(filtered, svc)
			}
		}
	}

	return filtered
}

func hasRequiredGroups(userGroups, requiredGroups []string) bool {
	if len(requiredGroups) == 0 {
		return true
	}

	for _, required := range requiredGroups {
		found := false
		for _, userGroup := range userGroups {
			if userGroup == required {
				found = true
				break
			}
		}
		if !found {
			return false
		}
	}
	return true
}

func TestServiceCache_UpdateService(t *testing.T) {
	c := NewServiceCache()

	original := &ServiceInfo{
		Name:       "test-service",
		Namespace:  "default",
		URL:        "https://test.example.com",
		Visibility: "public",
	}

	c.Add(original)

	updated := &ServiceInfo{
		Name:       "test-service",
		Namespace:  "default",
		URL:        "https://updated.example.com",
		Visibility: "authenticated",
	}

	c.Add(updated) // Adding again should update

	retrieved := c.Get("default", "test-service")
	if retrieved.URL != updated.URL {
		t.Errorf("expected URL %s, got %s", updated.URL, retrieved.URL)
	}
	if retrieved.Visibility != updated.Visibility {
		t.Errorf("expected visibility %s, got %s", updated.Visibility, retrieved.Visibility)
	}
}

func TestConvertNebariAppToServiceInfo(t *testing.T) {
	priority := 10
	app := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
		},
		Spec: appsv1.NebariAppSpec{
			FQDN: "test-app.example.com",
			LandingPage: &appsv1.LandingPageConfig{
				Visibility:     "authenticated",
				RequiredGroups: []string{"users", "developers"},
				Priority:       &priority,
				Display: appsv1.DisplayConfig{
					Title:       "Test Application",
					Description: "A test app",
					Category:    "development",
					Icon:        "https://example.com/icon.png",
				},
			},
		},
	}

	svc := convertNebariAppToServiceInfo(app)

	if svc.Name != "test-app" {
		t.Errorf("expected name test-app, got %s", svc.Name)
	}
	if svc.Namespace != "default" {
		t.Errorf("expected namespace default, got %s", svc.Namespace)
	}
	if svc.URL != "https://test-app.example.com" {
		t.Errorf("expected URL https://test-app.example.com, got %s", svc.URL)
	}
	if svc.Visibility != "authenticated" {
		t.Errorf("expected visibility authenticated, got %s", svc.Visibility)
	}
	if len(svc.RequiredGroups) != 2 {
		t.Errorf("expected 2 required groups, got %d", len(svc.RequiredGroups))
	}
	if *svc.Priority != 10 {
		t.Errorf("expected priority 10, got %d", *svc.Priority)
	}
	if svc.Category != "development" {
		t.Errorf("expected category development, got %s", svc.Category)
	}
}

// convertNebariAppToServiceInfo is a helper for testing
func convertNebariAppToServiceInfo(app *appsv1.NebariApp) *ServiceInfo {
	if app.Spec.LandingPage == nil {
		return nil
	}

	return &ServiceInfo{
		Name:           app.Name,
		Namespace:      app.Namespace,
		URL:            "https://" + app.Spec.FQDN,
		Title:          app.Spec.LandingPage.Display.Title,
		Description:    app.Spec.LandingPage.Display.Description,
		Icon:           app.Spec.LandingPage.Display.Icon,
		Category:       app.Spec.LandingPage.Display.Category,
		Visibility:     app.Spec.LandingPage.Visibility,
		RequiredGroups: app.Spec.LandingPage.RequiredGroups,
		Priority:       app.Spec.LandingPage.Priority,
	}
}
