package watcher

import (
	"context"
	"fmt"
	"time"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	landingcache "github.com/nebari-dev/nebari-operator/internal/landingpage/cache"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/cache"
	ctrl "sigs.k8s.io/controller-runtime"
	cachepkg "sigs.k8s.io/controller-runtime/pkg/cache"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

var log = ctrl.Log.WithName("watcher")

// NebariAppWatcher watches NebariApp resources and updates the service cache
type NebariAppWatcher struct {
	cache       *landingcache.ServiceCache
	kubeCache   cachepkg.Cache
	client      client.Client
	syncedCh    chan struct{}
	cacheSynced bool
}

// NewNebariAppWatcher creates a new NebariApp watcher
func NewNebariAppWatcher(config *rest.Config, scheme *runtime.Scheme, serviceCache *landingcache.ServiceCache) (*NebariAppWatcher, error) {
	kubeCache, err := cachepkg.New(config, cachepkg.Options{
		Scheme: scheme,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create cache: %w", err)
	}

	kubeClient, err := client.New(config, client.Options{
		Scheme: scheme,
		Cache: &client.CacheOptions{
			Reader: kubeCache,
		},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to create client: %w", err)
	}

	return &NebariAppWatcher{
		cache:       serviceCache,
		kubeCache:   kubeCache,
		client:      kubeClient,
		syncedCh:    make(chan struct{}),
		cacheSynced: false,
	}, nil
}

// Start starts watching NebariApp resources
func (w *NebariAppWatcher) Start(ctx context.Context) error {
	log.Info("Starting NebariApp watcher")

	go func() {
		if err := w.kubeCache.Start(ctx); err != nil {
			log.Error(err, "Failed to start cache")
		}
	}()

	if !w.kubeCache.WaitForCacheSync(ctx) {
		return fmt.Errorf("failed to sync cache")
	}

	log.Info("Cache synced, fetching initial NebariApp resources")

	if err := w.syncInitial(ctx); err != nil {
		return fmt.Errorf("failed to sync initial resources: %w", err)
	}

	close(w.syncedCh)
	w.cacheSynced = true

	log.Info("Initial sync complete, starting watch loop")
	return w.watch(ctx)
}

func (w *NebariAppWatcher) syncInitial(ctx context.Context) error {
	nebariAppList := &appsv1.NebariAppList{}
	if err := w.client.List(ctx, nebariAppList); err != nil {
		return fmt.Errorf("failed to list NebariApps: %w", err)
	}

	log.Info("Found NebariApp resources", "count", len(nebariAppList.Items))

	for i := range nebariAppList.Items {
		nebariApp := &nebariAppList.Items[i]
		if nebariApp.Spec.LandingPage != nil && nebariApp.Spec.LandingPage.Enabled {
			log.Info("Adding service to cache",
				"name", nebariApp.Name,
				"namespace", nebariApp.Namespace,
				"displayName", nebariApp.Spec.LandingPage.DisplayName,
			)
			w.cache.Add(nebariApp)
		}
	}

	return nil
}

func (w *NebariAppWatcher) watch(ctx context.Context) error {
	informer, err := w.kubeCache.GetInformer(ctx, &appsv1.NebariApp{})
	if err != nil {
		return fmt.Errorf("failed to get informer: %w", err)
	}

	_, err = informer.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    w.onAdd,
		UpdateFunc: w.onUpdate,
		DeleteFunc: w.onDelete,
	})
	if err != nil {
		return fmt.Errorf("failed to add event handler: %w", err)
	}

	<-ctx.Done()
	return nil
}

func (w *NebariAppWatcher) onAdd(obj interface{}) {
	nebariApp, ok := obj.(*appsv1.NebariApp)
	if !ok {
		log.Error(nil, "Failed to cast object to NebariApp", "type", fmt.Sprintf("%T", obj))
		return
	}

	if nebariApp.Spec.LandingPage != nil && nebariApp.Spec.LandingPage.Enabled {
		log.Info("Service added",
			"name", nebariApp.Name,
			"namespace", nebariApp.Namespace,
			"displayName", nebariApp.Spec.LandingPage.DisplayName,
		)
		w.cache.Add(nebariApp)
	}
}

func (w *NebariAppWatcher) onUpdate(oldObj, newObj interface{}) {
	nebariApp, ok := newObj.(*appsv1.NebariApp)
	if !ok {
		log.Error(nil, "Failed to cast object to NebariApp", "type", fmt.Sprintf("%T", newObj))
		return
	}

	uid := string(nebariApp.UID)

	if nebariApp.Spec.LandingPage != nil && nebariApp.Spec.LandingPage.Enabled {
		log.Info("Service updated",
			"name", nebariApp.Name,
			"namespace", nebariApp.Namespace,
			"displayName", nebariApp.Spec.LandingPage.DisplayName,
		)
		w.cache.Add(nebariApp)
	} else {
		log.Info("Service removed (landing page disabled)",
			"name", nebariApp.Name,
			"namespace", nebariApp.Namespace,
		)
		w.cache.Remove(uid)
	}
}

func (w *NebariAppWatcher) onDelete(obj interface{}) {
	nebariApp, ok := obj.(*appsv1.NebariApp)
	if !ok {
		log.Error(nil, "Failed to cast object to NebariApp", "type", fmt.Sprintf("%T", obj))
		return
	}

	uid := string(nebariApp.UID)
	log.Info("Service deleted",
		"name", nebariApp.Name,
		"namespace", nebariApp.Namespace,
	)
	w.cache.Remove(uid)
}

// WaitForCacheSync waits for the cache to be synced
func (w *NebariAppWatcher) WaitForCacheSync(ctx context.Context) bool {
	select {
	case <-w.syncedCh:
		return true
	case <-ctx.Done():
		return false
	case <-time.After(30 * time.Second):
		return false
	}
}
