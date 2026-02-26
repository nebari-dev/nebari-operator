package api

import (
	"encoding/json"
	"net/http"
	"os"
	"strings"

	"github.com/nebari-dev/nebari-operator/internal/webapi/auth"
	"github.com/nebari-dev/nebari-operator/internal/webapi/cache"
	"github.com/nebari-dev/nebari-operator/internal/webapi/pins"
	wshub "github.com/nebari-dev/nebari-operator/internal/webapi/websocket"
	ctrl "sigs.k8s.io/controller-runtime"
)

var log = ctrl.Log.WithName("api")

// Handler handles HTTP requests for the landing page API
type Handler struct {
	cache        *cache.ServiceCache
	jwtValidator *auth.JWTValidator
	enableAuth   bool
	hub          *wshub.Hub
	pinStore     *pins.PinStore
}

// NewHandler creates a new API handler.
// pinStore may be nil; when nil the /api/v1/pins endpoints return 501.
func NewHandler(serviceCache *cache.ServiceCache, jwtValidator *auth.JWTValidator, enableAuth bool, hub *wshub.Hub, pinStore *pins.PinStore) *Handler {
	return &Handler{
		cache:        serviceCache,
		jwtValidator: jwtValidator,
		enableAuth:   enableAuth,
		hub:          hub,
		pinStore:     pinStore,
	}
}

// Routes returns the HTTP router for the API
func (h *Handler) Routes() http.Handler {
	mux := http.NewServeMux()

	// API routes
	mux.HandleFunc("/api/v1/services", h.handleGetServices)
	mux.HandleFunc("/api/v1/services/", h.handleGetService) // matches /{namespace}/{name}
	mux.HandleFunc("/api/v1/categories", h.handleGetCategories)
	mux.HandleFunc("/api/v1/health", h.handleHealth)

	// WebSocket — real-time service updates
	if h.hub != nil {
		mux.HandleFunc("/api/v1/ws", h.hub.ServeWS)
	}

	// User pins — requires authentication; 501 when no PinStore is configured
	mux.HandleFunc("/api/v1/pins", h.handleGetPins)
	mux.HandleFunc("/api/v1/pins/", h.handlePinByUID)

	// Static file serving — only registered when /web/static is present (i.e. frontend
	// assets were built and included in the image). When running the API-only image
	// the root path simply returns 404 so API clients are unaffected.
	const staticDir = "/web/static"
	if _, err := os.Stat(staticDir); err == nil {
		fs := http.FileServer(http.Dir(staticDir))
		mux.Handle("/static/", http.StripPrefix("/static/", fs))
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/" || r.URL.Path == "/index.html" {
				http.ServeFile(w, r, staticDir+"/index.html")
			} else {
				http.NotFound(w, r)
			}
		})
	} else {
		mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "frontend not deployed", http.StatusNotFound)
		})
	}

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
		username := claims.PreferredUsername
		if username == "" {
			// Fall back to the JWT Subject (sub) when preferred_username is not
			// present in the access token (e.g. Keycloak lightweight tokens).
			username = claims.Subject
		}
		response.User = &UserInfo{
			Authenticated: true,
			Username:      username,
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

	// Path format: /api/v1/services/{namespace}/{name}
	rest := strings.TrimPrefix(r.URL.Path, "/api/v1/services/")
	parts := strings.SplitN(rest, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		http.Error(w, "Path must be /api/v1/services/{namespace}/{name}", http.StatusBadRequest)
		return
	}
	namespace, name := parts[0], parts[1]

	service := h.cache.GetByNamespacedName(namespace, name)
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

// PinsResponse is the response body for GET /api/v1/pins.
type PinsResponse struct {
	// Pins is the ordered list of pinned services (cached ServiceInfo snapshots).
	Pins []*cache.ServiceInfo `json:"pins"`
	// UIDs lists exactly which UIDs are stored, including those that are no longer
	// cached (e.g. the NebariApp was deleted).
	UIDs []string `json:"uids"`
}

// handleGetPins serves GET /api/v1/pins.
// Requires a valid JWT. Returns the caller's pinned services as full ServiceInfo
// objects, resolved from the live cache. Pins whose UIDs are no longer in the
// cache are included in UIDs but absent from Pins (graceful stale handling).
func (h *Handler) handleGetPins(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	if h.pinStore == nil {
		http.Error(w, "Pins feature not configured", http.StatusNotImplemented)
		return
	}
	claims, ok := h.requireAuth(w, r)
	if !ok {
		return
	}
	uids, err := h.pinStore.Get(claims.PreferredUsername)
	if err != nil {
		log.Error(err, "Failed to read pins", "user", claims.PreferredUsername)
		http.Error(w, "Internal server error", http.StatusInternalServerError)
		return
	}
	svcs := make([]*cache.ServiceInfo, 0, len(uids))
	for _, uid := range uids {
		if svc := h.cache.Get(uid); svc != nil {
			svcs = append(svcs, svc)
		}
	}
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(PinsResponse{Pins: svcs, UIDs: uids}); err != nil {
		log.Error(err, "Failed to encode pins response")
	}
}

// handlePinByUID serves PUT and DELETE /api/v1/pins/{uid}.
// PUT pins the service; DELETE unpins it. Both are idempotent.
// The {uid} segment is the NebariApp UID (UIDType string from status.serviceDiscovery).
func (h *Handler) handlePinByUID(w http.ResponseWriter, r *http.Request) {
	if h.pinStore == nil {
		http.Error(w, "Pins feature not configured", http.StatusNotImplemented)
		return
	}
	claims, ok := h.requireAuth(w, r)
	if !ok {
		return
	}
	uid := strings.TrimPrefix(r.URL.Path, "/api/v1/pins/")
	if uid == "" {
		http.Error(w, "UID is required: /api/v1/pins/{uid}", http.StatusBadRequest)
		return
	}
	switch r.Method {
	case http.MethodPut:
		if err := h.pinStore.Pin(claims.PreferredUsername, uid); err != nil {
			log.Error(err, "Failed to pin service", "user", claims.PreferredUsername, "uid", uid)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	case http.MethodDelete:
		if err := h.pinStore.Unpin(claims.PreferredUsername, uid); err != nil {
			log.Error(err, "Failed to unpin service", "user", claims.PreferredUsername, "uid", uid)
			http.Error(w, "Internal server error", http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	default:
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
	}
}

// requireAuth validates the JWT on r and returns the claims.
// On failure it writes an appropriate HTTP error and returns ok=false.
func (h *Handler) requireAuth(w http.ResponseWriter, r *http.Request) (*auth.Claims, bool) {
	if !h.enableAuth || h.jwtValidator == nil {
		// Auth disabled globally — return a synthetic claims with empty username
		// so that pin operations still work in dev/test mode (all stored under "").
		return &auth.Claims{PreferredUsername: "_anonymous"}, true
	}
	claims, ok := h.extractAndValidateJWT(r)
	if !ok {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return nil, false
	}
	// Identify the user by preferred_username; fall back to the JWT Subject (sub)
	// for Keycloak configurations that omit preferred_username from access tokens.
	if claims.PreferredUsername == "" {
		claims.PreferredUsername = claims.Subject
	}
	if claims.PreferredUsername == "" {
		http.Error(w, "JWT missing user identity claim (preferred_username or sub)", http.StatusUnauthorized)
		return nil, false
	}
	return claims, true
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
