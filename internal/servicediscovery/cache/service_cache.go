package cache

import (
	"sort"
	"sync"
	"time"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
)

// ServiceInfo represents a service that appears on the landing page
type ServiceInfo struct {
	UID            string        `json:"uid"`
	Name           string        `json:"name"`
	Namespace      string        `json:"namespace"`
	DisplayName    string        `json:"displayName"`
	Description    string        `json:"description"`
	URL            string        `json:"url"`
	Icon           string        `json:"icon"`
	Category       string        `json:"category"`
	Priority       int           `json:"priority"`
	Visibility     string        `json:"visibility"`
	RequiredGroups []string      `json:"requiredGroups,omitempty"`
	Health         *HealthStatus `json:"health,omitempty"`
}

// HealthStatus represents the health status of a service
type HealthStatus struct {
	Status    string     `json:"status"` // healthy, unhealthy, unknown
	LastCheck *time.Time `json:"lastCheck,omitempty"`
	Message   string     `json:"message,omitempty"`
}

// ServiceCache maintains an in-memory cache of services
type ServiceCache struct {
	mu       sync.RWMutex
	services map[string]*ServiceInfo // keyed by UID
}

// NewServiceCache creates a new service cache
func NewServiceCache() *ServiceCache {
	return &ServiceCache{
		services: make(map[string]*ServiceInfo),
	}
}

// Add adds or updates a service in the cache.
// It prefers status.ServiceDiscovery (pre-computed by the controller) when present
// and enabled, falling back to deriving fields directly from spec for backward
// compatibility (e.g., before the controller has written the status field).
func (c *ServiceCache) Add(nebariApp *appsv1.NebariApp) {
	if nebariApp.Spec.LandingPage == nil || !nebariApp.Spec.LandingPage.Enabled {
		c.Remove(string(nebariApp.UID))
		return
	}

	uid := string(nebariApp.UID)

	var (
		displayName    string
		description    string
		url            string
		icon           string
		category       string
		priority       = 100
		visibility     = "authenticated"
		requiredGroups []string
	)

	if sd := nebariApp.Status.ServiceDiscovery; sd != nil && sd.Enabled {
		// Use the controller's pre-computed, URL-resolved view.
		displayName = sd.DisplayName
		description = sd.Description
		url = sd.URL
		icon = sd.Icon
		category = sd.Category
		if sd.Priority != 0 {
			priority = sd.Priority
		}
		if sd.Visibility != "" {
			visibility = sd.Visibility
		}
		requiredGroups = sd.RequiredGroups
	} else {
		// Fall back to spec-derived values (controller hasn't written status yet).
		lp := nebariApp.Spec.LandingPage
		displayName = lp.DisplayName
		description = lp.Description
		url = buildURL(nebariApp)
		icon = lp.Icon
		category = lp.Category
		if lp.Priority != nil {
			priority = *lp.Priority
		}
		if lp.Visibility != "" {
			visibility = lp.Visibility
		}
		requiredGroups = lp.RequiredGroups
	}

	service := &ServiceInfo{
		UID:            uid,
		Name:           nebariApp.Name,
		Namespace:      nebariApp.Namespace,
		DisplayName:    displayName,
		Description:    description,
		URL:            url,
		Icon:           icon,
		Category:       category,
		Priority:       priority,
		Visibility:     visibility,
		RequiredGroups: requiredGroups,
		Health:         c.preserveHealthStatus(uid),
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	c.services[uid] = service
}

// Remove removes a service from the cache
func (c *ServiceCache) Remove(uid string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.services, uid)
}

// Get retrieves a service by UID
func (c *ServiceCache) Get(uid string) *ServiceInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return c.services[uid]
}

// GetByNamespacedName retrieves a service by namespace and name.
func (c *ServiceCache) GetByNamespacedName(namespace, name string) *ServiceInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()
	for _, svc := range c.services {
		if svc.Namespace == namespace && svc.Name == name {
			return svc
		}
	}
	return nil
}

// GetAll returns all services as a slice
func (c *ServiceCache) GetAll() []*ServiceInfo {
	c.mu.RLock()
	defer c.mu.RUnlock()

	services := make([]*ServiceInfo, 0, len(c.services))
	for _, service := range c.services {
		services = append(services, service)
	}

	sort.Slice(services, func(i, j int) bool {
		if services[i].Priority != services[j].Priority {
			return services[i].Priority < services[j].Priority
		}
		return services[i].Name < services[j].Name
	})

	return services
}

// GetCategories returns a unique sorted list of all categories
func (c *ServiceCache) GetCategories() []string {
	c.mu.RLock()
	defer c.mu.RUnlock()

	categoryMap := make(map[string]bool)
	for _, service := range c.services {
		if service.Category != "" {
			categoryMap[service.Category] = true
		}
	}

	categories := make([]string, 0, len(categoryMap))
	for category := range categoryMap {
		categories = append(categories, category)
	}

	sort.Strings(categories)
	return categories
}

// UpdateHealth updates the health status for a service
func (c *ServiceCache) UpdateHealth(uid string, status *HealthStatus) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if service, exists := c.services[uid]; exists {
		service.Health = status
	}
}

func (c *ServiceCache) preserveHealthStatus(uid string) *HealthStatus {
	if existing := c.services[uid]; existing != nil && existing.Health != nil {
		return existing.Health
	}
	return &HealthStatus{
		Status: "unknown",
	}
}

func buildURL(nebariApp *appsv1.NebariApp) string {
	if nebariApp.Spec.LandingPage.ExternalUrl != "" {
		return nebariApp.Spec.LandingPage.ExternalUrl
	}

	scheme := "https"
	if nebariApp.Spec.Routing != nil && nebariApp.Spec.Routing.TLS != nil {
		if nebariApp.Spec.Routing.TLS.Enabled != nil && !*nebariApp.Spec.Routing.TLS.Enabled {
			scheme = "http"
		}
	}

	return scheme + "://" + nebariApp.Spec.Hostname
}
