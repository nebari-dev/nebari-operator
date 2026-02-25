package api

import (
	"encoding/json"
	"net/http"
	"strings"

	"github.com/nebari-dev/nebari-operator/internal/landingpage/auth"
	"github.com/nebari-dev/nebari-operator/internal/landingpage/cache"
	ctrl "sigs.k8s.io/controller-runtime"
)

var log = ctrl.Log.WithName("api")

// Handler handles HTTP requests for the landing page API
type Handler struct {
	cache        *cache.ServiceCache
	jwtValidator *auth.JWTValidator
	enableAuth   bool
}

// NewHandler creates a new API handler
func NewHandler(serviceCache *cache.ServiceCache, jwtValidator *auth.JWTValidator, enableAuth bool) *Handler {
	return &Handler{
		cache:        serviceCache,
		jwtValidator: jwtValidator,
		enableAuth:   enableAuth,
	}
}

// Routes returns the HTTP router for the API
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("/api/v1/services", h.handleGetServices)
	mux.HandleFunc("/api/v1/services/", h.handleGetService)
	mux.HandleFunc("/api/v1/categories", h.handleGetCategories)
	mux.HandleFunc("/api/v1/health", h.handleHealth)

	// Static file serving
	fs := http.FileServer(http.Dir("/web/static"))
	mux.Handle("/static/", http.StripPrefix("/static/", fs))

	// Serve index.html for root and any unknown paths (SPA fallback)
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" || r.URL.Path == "/index.html" {
			http.ServeFile(w, r, "/web/static/index.html")
		} else {
			http.NotFound(w, r)
		}
	})

	return corsMiddleware(mux)
}

// ServiceResponse is the response format for GET /api/v1/services
type ServiceResponse struct {
	Services struct {
		Public        []*cache.ServiceInfo `json:"public"`
		Authenticated []*cache.ServiceInfo `json:"authenticated"`
		Private       []*cache.ServiceInfo `json:"private"`
	} `json:"services"`
	Categories []string  `json:"categories"`
	User       *UserInfo `json:"user,omitempty"`
}

// UserInfo contains information about the authenticated user
type UserInfo struct {
	Authenticated bool     `json:"authenticated"`
	Username      string   `json:"username,omitempty"`
	Email         string   `json:"email,omitempty"`
	Name          string   `json:"name,omitempty"`
	Groups        []string `json:"groups,omitempty"`
}

func (h *Handler) handleGetServices(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	response := ServiceResponse{
		Categories: h.cache.GetCategories(),
	}

	response.Services.Public = make([]*cache.ServiceInfo, 0)
	response.Services.Authenticated = make([]*cache.ServiceInfo, 0)
	response.Services.Private = make([]*cache.ServiceInfo, 0)

	claims, authenticated := h.extractAndValidateJWT(r)

	allServices := h.cache.GetAll()

	for _, service := range allServices {
		switch service.Visibility {
		case "public":
			response.Services.Public = append(response.Services.Public, service)

		case "authenticated":
			if authenticated {
				response.Services.Authenticated = append(response.Services.Authenticated, service)
			}

		case "private":
			if authenticated && h.hasRequiredGroups(claims.Groups, service.RequiredGroups) {
				response.Services.Private = append(response.Services.Private, service)
			}

		default:
			if authenticated {
				response.Services.Authenticated = append(response.Services.Authenticated, service)
			}
		}
	}

	if authenticated {
		response.User = &UserInfo{
			Authenticated: true,
			Username:      claims.PreferredUsername,
			Email:         claims.Email,
			Name:          claims.Name,
			Groups:        claims.Groups,
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(response); err != nil {
		log.Error(err, "Failed to encode response")
	}
}

func (h *Handler) handleGetService(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	uid := strings.TrimPrefix(r.URL.Path, "/api/v1/services/")
	if uid == "" {
		http.Error(w, "Service UID required", http.StatusBadRequest)
		return
	}

	service := h.cache.Get(uid)
	if service == nil {
		http.Error(w, "Service not found", http.StatusNotFound)
		return
	}

	claims, authenticated := h.extractAndValidateJWT(r)

	if !h.canAccessService(service, authenticated, claims) {
		http.Error(w, "Forbidden", http.StatusForbidden)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(service); err != nil {
		log.Error(err, "Failed to encode service")
	}
}

func (h *Handler) handleGetCategories(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	categories := h.cache.GetCategories()

	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(map[string]interface{}{
		"categories": categories,
	}); err != nil {
		log.Error(err, "Failed to encode categories")
	}
}

func (h *Handler) handleHealth(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	if err := json.NewEncoder(w).Encode(map[string]string{
		"status": "healthy",
	}); err != nil {
		log.Error(err, "Failed to encode health response")
	}
}

func (h *Handler) extractAndValidateJWT(r *http.Request) (*auth.Claims, bool) {
	if !h.enableAuth || h.jwtValidator == nil {
		return nil, false
	}

	authHeader := r.Header.Get("Authorization")
	if authHeader == "" {
		return nil, false
	}

	parts := strings.Split(authHeader, " ")
	if len(parts) != 2 || parts[0] != "Bearer" {
		log.Info("Invalid Authorization header format")
		return nil, false
	}

	tokenString := parts[1]

	claims, err := h.jwtValidator.ValidateToken(tokenString)
	if err != nil {
		log.Info("JWT validation failed", "error", err.Error())
		return nil, false
	}

	return claims, true
}

func (h *Handler) canAccessService(service *cache.ServiceInfo, authenticated bool, claims *auth.Claims) bool {
	switch service.Visibility {
	case "public":
		return true

	case "authenticated":
		return authenticated

	case "private":
		if !authenticated {
			return false
		}
		return h.hasRequiredGroups(claims.Groups, service.RequiredGroups)

	default:
		return authenticated
	}
}

func (h *Handler) hasRequiredGroups(userGroups, requiredGroups []string) bool {
	if len(requiredGroups) == 0 {
		return true
	}

	for _, required := range requiredGroups {
		for _, userGroup := range userGroups {
			if userGroup == required {
				return true
			}
		}
	}

	return false
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
