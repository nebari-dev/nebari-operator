package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/log/zap"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/landingpage/api"
	"github.com/nebari-dev/nebari-operator/internal/landingpage/auth"
	"github.com/nebari-dev/nebari-operator/internal/landingpage/cache"
	"github.com/nebari-dev/nebari-operator/internal/landingpage/health"
	"github.com/nebari-dev/nebari-operator/internal/landingpage/watcher"
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
		kubeconfig     string
		keycloakURL    string
		keycloakRealm  string
		enableAuth     bool
		healthInterval int
	)

	flag.IntVar(&port, "port", 8080, "Port to listen on")
	flag.StringVar(&kubeconfig, "kubeconfig", "", "Path to kubeconfig file (optional, uses in-cluster config if not provided)")
	flag.StringVar(&keycloakURL, "keycloak-url", "", "Keycloak base URL (e.g., https://keycloak.example.com)")
	flag.StringVar(&keycloakRealm, "keycloak-realm", "main", "Keycloak realm name")
	flag.BoolVar(&enableAuth, "enable-auth", false, "Enable JWT authentication and authorization")
	flag.IntVar(&healthInterval, "health-interval", 30, "Health check interval in seconds")

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

	config, err := getKubeConfig(kubeconfig)
	if err != nil {
		setupLog.Error(err, "Failed to get kubeconfig")
		os.Exit(1)
	}

	serviceCache := cache.NewServiceCache()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	nebariAppWatcher, err := watcher.NewNebariAppWatcher(config, scheme, serviceCache)
	if err != nil {
		setupLog.Error(err, "Failed to create NebariApp watcher")
		os.Exit(1)
	}

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

	handler := api.NewHandler(serviceCache, jwtValidator, enableAuth)

	mux := handler.Routes()

	server := &http.Server{
		Addr:         fmt.Sprintf(":%d", port),
		Handler:      mux,
		ReadTimeout:  15 * time.Second,
		WriteTimeout: 15 * time.Second,
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

	setupLog.Info("Server stopped")
}

func getKubeConfig(kubeconfig string) (*rest.Config, error) {
	if kubeconfig != "" {
		return clientcmd.BuildConfigFromFlags("", kubeconfig)
	}
	return rest.InClusterConfig()
}
