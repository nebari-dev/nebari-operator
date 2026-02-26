package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"syscall"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/webapi/api"
	"github.com/nebari-dev/nebari-operator/internal/webapi/auth"
	"github.com/nebari-dev/nebari-operator/internal/webapi/cache"
	"github.com/nebari-dev/nebari-operator/internal/webapi/health"
	"github.com/nebari-dev/nebari-operator/internal/webapi/pins"
	"github.com/nebari-dev/nebari-operator/internal/webapi/watcher"
	wshub "github.com/nebari-dev/nebari-operator/internal/webapi/websocket"
)

var (
	scheme   = runtime.NewScheme()
	setupLog = ctrl.Log.WithName("setup")
)

func init() {
	_ = clientgoscheme.AddToScheme(scheme)
	_ = appsv1.AddToScheme(scheme)
}

func main() {
	var (
		port           int
		keycloakURL    string
		keycloakRealm  string
		enableAuth     bool
		healthInterval int
		pinsDBPath     string
	)

	// Flags fall back to environment variables so the binary works naturally when
	// deployed as a Kubernetes Pod using env: in the Deployment manifest.
	// Precedence: CLI flag > environment variable > built-in default.
	flag.IntVar(&port, "port", envInt("PORT", 8080),
		"Port to listen on (env: PORT)")
	// Note: controller-runtime registers --kubeconfig in its own init(); use ctrl.GetConfig() below.
	flag.StringVar(&keycloakURL, "keycloak-url", os.Getenv("KEYCLOAK_URL"),
		"Keycloak base URL, e.g. https://keycloak.example.com (env: KEYCLOAK_URL)")
	flag.StringVar(&keycloakRealm, "keycloak-realm", envStr("KEYCLOAK_REALM", "main"),
		"Keycloak realm name (env: KEYCLOAK_REALM)")
	flag.BoolVar(&enableAuth, "enable-auth", envBool("ENABLE_AUTH", false),
		"Enable JWT authentication and authorization (env: ENABLE_AUTH)")
	flag.IntVar(&healthInterval, "health-interval", envInt("HEALTH_INTERVAL", 30),
		"Health check interval in seconds (env: HEALTH_INTERVAL)")
	flag.StringVar(&pinsDBPath, "pins-db", envStr("PINS_DB_PATH", "/data/pins.db"),
		"Path to the bbolt database file for user pin storage (env: PINS_DB_PATH)")

	opts := zap.Options{
		Development: true,
	}
	opts.BindFlags(flag.CommandLine)
	flag.Parse()

	ctrl.SetLogger(zap.New(zap.UseFlagOptions(&opts)))

	setupLog.Info("Starting Nebari Landing Page API Server",
		"port", port,
		"authEnabled", enableAuth,
		"healthInterval", healthInterval,
	)

	config, err := ctrl.GetConfig()
	if err != nil {
		setupLog.Error(err, "Failed to get kubeconfig")
		os.Exit(1)
	}

	serviceCache := cache.NewServiceCache()
	hub := wshub.NewHub()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nebariAppWatcher, err := watcher.NewNebariAppWatcher(config, scheme, serviceCache)
	if err != nil {
		setupLog.Error(err, "Failed to create NebariApp watcher")
		os.Exit(1)
	}
	nebariAppWatcher.SetPublisher(hub)

	go func() {
		if err := nebariAppWatcher.Start(ctx); err != nil {
			setupLog.Error(err, "Failed to start NebariApp watcher")
			os.Exit(1)
		}
	}()

	setupLog.Info("Waiting for cache to sync...")
	if !nebariAppWatcher.WaitForCacheSync(ctx) {
		setupLog.Error(nil, "Failed to sync cache")
		os.Exit(1)
	}
	setupLog.Info("Cache synced successfully")

	var jwtValidator *auth.JWTValidator
	if enableAuth {
		if keycloakURL == "" {
			setupLog.Error(nil, "keycloak-url is required when auth is enabled")
			os.Exit(1)
		}
		jwtValidator, err = auth.NewJWTValidator(keycloakURL, keycloakRealm)
		if err != nil {
			setupLog.Error(err, "Failed to create JWT validator")
			os.Exit(1)
		}
		setupLog.Info("JWT validation enabled", "keycloakURL", keycloakURL, "realm", keycloakRealm)
	} else {
		setupLog.Info("JWT validation disabled - all requests will be treated as unauthenticated")
	}

	healthChecker := health.NewHealthChecker(serviceCache, time.Duration(healthInterval)*time.Second)
	go healthChecker.Start(ctx)

	// Open the pin store (bbolt). The database file is created if it doesn't exist.
	// A nil store disables the /api/v1/pins endpoints gracefully.
	var pinStore *pins.PinStore
	if pinsDBPath != "" {
		ps, err := pins.NewPinStore(pinsDBPath)
		if err != nil {
			setupLog.Error(err, "Failed to open pin store", "path", pinsDBPath)
			os.Exit(1)
		}
		pinStore = ps
		setupLog.Info("Pin store opened", "path", pinsDBPath)
	} else {
		setupLog.Info("PINS_DB_PATH is empty — pin endpoints disabled")
	}

	handler := api.NewHandler(serviceCache, jwtValidator, enableAuth, hub, pinStore)

	mux := handler.Routes()

	server := &http.Server{
		Addr:        fmt.Sprintf(":%d", port),
		Handler:     mux,
		ReadTimeout: 15 * time.Second,
		// WriteTimeout must be 0 when WebSocket connections are active — a non-zero
		// value causes the http.Server to cancel upgraded connections after the timeout
		// fires, disconnecting all WS clients even when the connection is healthy.
		// Per-frame write deadlines are enforced inside the Hub.Broadcast instead.
		WriteTimeout: 0,
		IdleTimeout:  60 * time.Second,
	}

	go func() {
		setupLog.Info("Starting HTTP server", "address", server.Addr)
		if err := server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			setupLog.Error(err, "HTTP server failed")
			os.Exit(1)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, os.Interrupt, syscall.SIGTERM)
	<-sigCh

	setupLog.Info("Shutting down gracefully...")

	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()

	if err := server.Shutdown(shutdownCtx); err != nil {
		setupLog.Error(err, "Server shutdown failed")
	}

	if pinStore != nil {
		if err := pinStore.Close(); err != nil {
			setupLog.Error(err, "Failed to close pin store")
		}
	}

	setupLog.Info("Server stopped")
}

// envStr returns the value of the named environment variable, or def if unset/empty.
func envStr(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// envInt returns the int value of the named environment variable, or def if unset/invalid.
func envInt(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return def
	}
	return n
}

// envBool returns the bool value of the named environment variable, or def if unset/invalid.
// Accepts "1", "true", "yes" (case-insensitive) as true.
func envBool(name string, def bool) bool {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	b, err := strconv.ParseBool(v)
	if err != nil {
		return def
	}
	return b
}
