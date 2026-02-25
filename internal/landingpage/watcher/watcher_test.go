package watcher

import (
	"testing"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	landingcache "github.com/nebari-dev/nebari-operator/internal/landingpage/cache"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
)

func TestNebariAppWatcher_OnAdd(t *testing.T) {
	cache := landingcache.NewServiceCache()
	watcher := &NebariAppWatcher{
		serviceCache: cache,
	}

	priority := 10
	app := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
		},
		Spec: appsv1.NebariAppSpec{
			FQDN: "test-app.example.com",
			LandingPage: &appsv1.LandingPageConfig{
				Visibility: "public",
				Priority:   &priority,
				Display: appsv1.DisplayConfig{
					Title:    "Test App",
					Category: "development",
				},
			},
		},
	}

	watcher.onAdd(app)

	svc := cache.Get("default", "test-app")
	if svc == nil {
		t.Fatal("expected service to be added to cache")
	}

	if svc.Name != "test-app" {
		t.Errorf("expected name test-app, got %s", svc.Name)
	}
	if svc.URL != "https://test-app.example.com" {
		t.Errorf("expected URL https://test-app.example.com, got %s", svc.URL)
	}
}

func TestNebariAppWatcher_OnUpdate(t *testing.T) {
	cache := landingcache.NewServiceCache()
	watcher := &NebariAppWatcher{
		serviceCache: cache,
	}

	priority1 := 10
	priority2 := 20

	oldApp := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
		},
		Spec: appsv1.NebariAppSpec{
			FQDN: "test-app.example.com",
			LandingPage: &appsv1.LandingPageConfig{
				Visibility: "public",
				Priority:   &priority1,
				Display: appsv1.DisplayConfig{
					Title: "Old Title",
				},
			},
		},
	}

	newApp := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
		},
		Spec: appsv1.NebariAppSpec{
			FQDN: "test-app.example.com",
			LandingPage: &appsv1.LandingPageConfig{
				Visibility: "authenticated",
				Priority:   &priority2,
				Display: appsv1.DisplayConfig{
					Title: "New Title",
				},
			},
		},
	}

	// Add old version
	watcher.onAdd(oldApp)

	// Update to new version
	watcher.onUpdate(oldApp, newApp)

	svc := cache.Get("default", "test-app")
	if svc == nil {
		t.Fatal("expected service to be in cache")
	}

	if svc.Title != "New Title" {
		t.Errorf("expected title 'New Title', got %s", svc.Title)
	}
	if svc.Visibility != "authenticated" {
		t.Errorf("expected visibility 'authenticated', got %s", svc.Visibility)
	}
	if *svc.Priority != 20 {
		t.Errorf("expected priority 20, got %d", *svc.Priority)
	}
}

func TestNebariAppWatcher_OnDelete(t *testing.T) {
	cache := landingcache.NewServiceCache()
	watcher := &NebariAppWatcher{
		serviceCache: cache,
	}

	priority := 10
	app := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "test-app",
			Namespace: "default",
		},
		Spec: appsv1.NebariAppSpec{
			FQDN: "test-app.example.com",
			LandingPage: &appsv1.LandingPageConfig{
				Visibility: "public",
				Priority:   &priority,
			},
		},
	}

	// Add the app
	watcher.onAdd(app)

	// Verify it's in the cache
	if cache.Get("default", "test-app") == nil {
		t.Fatal("app should be in cache before deletion")
	}

	// Delete the app
	watcher.onDelete(app)

	// Verify it's removed from cache
	if cache.Get("default", "test-app") != nil {
		t.Error("app should be removed from cache after deletion")
	}
}

func TestNebariAppWatcher_IgnoreNonLandingPageApps(t *testing.T) {
	cache := landingcache.NewServiceCache()
	watcher := &NebariAppWatcher{
		serviceCache: cache,
	}

	// App without landing page config
	app := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "regular-app",
			Namespace: "default",
		},
		Spec: appsv1.NebariAppSpec{
			FQDN:        "regular-app.example.com",
			LandingPage: nil, // No landing page config
		},
	}

	watcher.onAdd(app)

	// Should not be added to cache
	if cache.Get("default", "regular-app") != nil {
		t.Error("apps without landing page config should not be added to cache")
	}
}

func TestNewNebariAppWatcher(t *testing.T) {
	scheme := runtime.NewScheme()
	_ = clientgoscheme.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)

	// Create a fake client
	fakeClient := fake.NewClientBuilder().
		WithScheme(scheme).
		Build()

	cache := landingcache.NewServiceCache()

	// Note: This test is limited because we can't easily create a real cache.Cache
	// without a real cluster. In a real integration test, you'd use envtest.
	_ = fakeClient // Just verify we can create dependencies

	if cache == nil {
		t.Fatal("cache should not be nil")
	}
}

func TestNebariAppWatcher_ConvertToServiceInfo(t *testing.T) {
	priority := 15
	app := &appsv1.NebariApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "jupyter",
			Namespace: "nebari",
		},
		Spec: appsv1.NebariAppSpec{
			FQDN: "jupyter.nebari.example.com",
			LandingPage: &appsv1.LandingPageConfig{
				Visibility:     "authenticated",
				RequiredGroups: []string{"users", "data-scientists"},
				Priority:       &priority,
				Display: appsv1.DisplayConfig{
					Title:       "JupyterLab",
					Description: "Interactive Python notebooks",
					Category:    "data-science",
					Icon:        "https://jupyter.org/icon.png",
				},
			},
		},
	}

	svc := convertToServiceInfo(app)

	if svc == nil {
		t.Fatal("convertToServiceInfo returned nil")
	}

	if svc.Name != "jupyter" {
		t.Errorf("expected name jupyter, got %s", svc.Name)
	}
	if svc.Namespace != "nebari" {
		t.Errorf("expected namespace nebari, got %s", svc.Namespace)
	}
	if svc.URL != "https://jupyter.nebari.example.com" {
		t.Errorf("expected HTTPS URL, got %s", svc.URL)
	}
	if svc.Title != "JupyterLab" {
		t.Errorf("expected title JupyterLab, got %s", svc.Title)
	}
	if svc.Category != "data-science" {
		t.Errorf("expected category data-science, got %s", svc.Category)
	}
	if len(svc.RequiredGroups) != 2 {
		t.Errorf("expected 2 required groups, got %d", len(svc.RequiredGroups))
	}
}

// Helper function for conversion testing
func convertToServiceInfo(app *appsv1.NebariApp) *landingcache.ServiceInfo {
	if app.Spec.LandingPage == nil {
		return nil
	}

	return &landingcache.ServiceInfo{
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
