/*
Copyright 2026, OpenTeams.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package routing

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
	gatewayv1 "sigs.k8s.io/gateway-api/apis/v1"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/conditions"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/constants"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/naming"
)

// RoutingReconciler handles HTTPRoute generation and management for NebariApp resources
type RoutingReconciler struct {
	Client   client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// ReconcileRouting creates or updates the HTTPRoute for a NebariApp.
// tlsListenerName is the name of the per-app TLS listener on the Gateway,
// provided by the TLS reconciler. When non-empty and TLS is enabled, the
// HTTPRoute will target this listener instead of the default "https" listener.
func (r *RoutingReconciler) ReconcileRouting(ctx context.Context, nebariApp *appsv1.NebariApp, tlsListenerName string) error {
	logger := log.FromContext(ctx)

	// Determine which gateway to use
	gatewayName := naming.GatewayName(nebariApp)
	logger.Info("Reconciling routing", "gateway", gatewayName, "hostname", nebariApp.Spec.Hostname)

	// Verify gateway exists
	if err := r.validateGateway(ctx, gatewayName); err != nil {
		logger.Error(err, "Gateway validation failed")
		r.Recorder.Event(nebariApp, corev1.EventTypeWarning, appsv1.EventReasonGatewayNotFound, err.Error())
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeRoutingReady, metav1.ConditionFalse,
			appsv1.EventReasonGatewayNotFound, err.Error())
		return err
	}

	// Generate desired HTTPRoute
	desiredRoute, err := r.buildHTTPRoute(nebariApp, gatewayName, tlsListenerName)
	if err != nil {
		logger.Error(err, "Failed to build HTTPRoute")
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeRoutingReady, metav1.ConditionFalse,
			"BuildFailed", fmt.Sprintf("Failed to build HTTPRoute: %v", err))
		return err
	}

	// Check if HTTPRoute already exists
	existingRoute := &gatewayv1.HTTPRoute{}
	routeKey := client.ObjectKey{
		Name:      desiredRoute.Name,
		Namespace: desiredRoute.Namespace,
	}

	err = r.Client.Get(ctx, routeKey, existingRoute)
	if err != nil {
		if errors.IsNotFound(err) {
			// Create new HTTPRoute
			if err := r.Client.Create(ctx, desiredRoute); err != nil {
				logger.Error(err, "Failed to create HTTPRoute")
				conditions.SetCondition(nebariApp, appsv1.ConditionTypeRoutingReady, metav1.ConditionFalse,
					"CreationFailed", fmt.Sprintf("Failed to create HTTPRoute: %v", err))
				return err
			}
			logger.Info("Created HTTPRoute", "name", desiredRoute.Name)
			r.Recorder.Event(nebariApp, corev1.EventTypeNormal, appsv1.EventReasonHTTPRouteCreated,
				fmt.Sprintf("Created HTTPRoute %s", desiredRoute.Name))

			conditions.SetCondition(nebariApp, appsv1.ConditionTypeRoutingReady, metav1.ConditionTrue,
				"HTTPRouteCreated", "HTTPRoute created successfully")
			return nil
		}
		return err
	}

	// Update existing HTTPRoute
	existingRoute.Spec = desiredRoute.Spec
	if err := r.Client.Update(ctx, existingRoute); err != nil {
		// Conflict errors are expected when multiple reconciliations happen concurrently
		// Return nil to avoid error logging - the controller will naturally retry
		if errors.IsConflict(err) {
			logger.V(1).Info("HTTPRoute update conflict, will retry", "name", existingRoute.Name)
			return nil
		}
		logger.Error(err, "Failed to update HTTPRoute")
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeRoutingReady, metav1.ConditionFalse,
			"UpdateFailed", fmt.Sprintf("Failed to update HTTPRoute: %v", err))
		return err
	}

	logger.Info("Updated HTTPRoute", "name", existingRoute.Name)
	r.Recorder.Event(nebariApp, corev1.EventTypeNormal, appsv1.EventReasonHTTPRouteUpdated,
		fmt.Sprintf("Updated HTTPRoute %s", existingRoute.Name))

	conditions.SetCondition(nebariApp, appsv1.ConditionTypeRoutingReady, metav1.ConditionTrue,
		"HTTPRouteReady", "HTTPRoute is configured and ready")

	return nil
}

// CleanupHTTPRoute removes the HTTPRoute for a NebariApp
func (r *RoutingReconciler) CleanupHTTPRoute(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	logger := log.FromContext(ctx)

	routeName := naming.HTTPRouteName(nebariApp)
	route := &gatewayv1.HTTPRoute{}
	routeKey := client.ObjectKey{
		Name:      routeName,
		Namespace: nebariApp.Namespace,
	}

	if err := r.Client.Get(ctx, routeKey, route); err != nil {
		if errors.IsNotFound(err) {
			// Already deleted
			return nil
		}
		return err
	}

	if err := r.Client.Delete(ctx, route); err != nil {
		logger.Error(err, "Failed to delete HTTPRoute")
		return err
	}

	logger.Info("Deleted HTTPRoute", "name", routeName)
	r.Recorder.Event(nebariApp, corev1.EventTypeNormal, appsv1.EventReasonHTTPRouteDeleted,
		fmt.Sprintf("Deleted HTTPRoute %s", routeName))

	return nil
}

// buildHTTPRoute generates an HTTPRoute resource from NebariApp spec.
// tlsListenerName overrides the default "https" section name when TLS is enabled
// and a per-app TLS listener has been created by the TLS reconciler.
func (r *RoutingReconciler) buildHTTPRoute(nebariApp *appsv1.NebariApp, gatewayName string, tlsListenerName string) (*gatewayv1.HTTPRoute, error) {
	routeName := naming.HTTPRouteName(nebariApp)
	namespace := gatewayv1.Namespace(constants.GatewayNamespace)

	// Determine which Gateway listener to use
	// Priority: tlsListenerName (from TLS reconciler) > TLS enabled ("https") > TLS disabled ("http")
	sectionName := gatewayv1.SectionName("https")
	tlsEnabled := true
	if nebariApp.Spec.Routing != nil && nebariApp.Spec.Routing.TLS != nil && nebariApp.Spec.Routing.TLS.Enabled != nil && !*nebariApp.Spec.Routing.TLS.Enabled {
		sectionName = gatewayv1.SectionName("http")
		tlsEnabled = false
	}
	if tlsListenerName != "" && tlsEnabled {
		sectionName = gatewayv1.SectionName(tlsListenerName)
	}

	// Build HTTPRoute annotations: start with user-supplied annotations from the
	// routing spec, then apply operator-managed ones so they always take precedence.
	httpRouteAnnotations := map[string]string{}
	if nebariApp.Spec.Routing != nil {
		for k, v := range nebariApp.Spec.Routing.Annotations {
			httpRouteAnnotations[k] = v
		}
	}
	httpRouteAnnotations["nebari.dev/tls-enabled"] = fmt.Sprintf("%t", tlsEnabled)

	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      routeName,
			Namespace: nebariApp.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "nebariapp",
				"app.kubernetes.io/instance":   nebariApp.Name,
				"app.kubernetes.io/managed-by": "nebari-operator",
			},
			Annotations: httpRouteAnnotations,
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{
					{
						Name:        gatewayv1.ObjectName(gatewayName),
						Namespace:   &namespace,
						SectionName: &sectionName,
					},
				},
			},
			Hostnames: []gatewayv1.Hostname{
				gatewayv1.Hostname(nebariApp.Spec.Hostname),
			},
			Rules: r.buildHTTPRouteRules(nebariApp),
		},
	}

	// Set owner reference for garbage collection
	if err := controllerutil.SetControllerReference(nebariApp, route, r.Scheme); err != nil {
		return nil, fmt.Errorf("failed to set controller reference on HTTPRoute: %w", err)
	}

	return route, nil
}

// buildHTTPRouteRules generates HTTPRoute rules based on NebariApp routes
func (r *RoutingReconciler) buildHTTPRouteRules(nebariApp *appsv1.NebariApp) []gatewayv1.HTTPRouteRule {
	// Get routes from routing config if specified
	var routes []appsv1.RouteMatch
	if nebariApp.Spec.Routing != nil {
		routes = nebariApp.Spec.Routing.Routes
	}

	// With no routes the rule has no matches, and Gateway API matches every
	// path ("/" PathPrefix).
	return buildRouteRules(routes, gatewayv1.PathMatchPathPrefix, r.buildBackendRefs(nebariApp))
}

// buildRouteRules turns route matches into HTTPRoute rules that all forward to
// backendRefs. Matches without timeouts share one rule, so an app that sets no
// timeouts gets the same single rule as before. Matches with timeouts are
// grouped by their timeouts, one rule per distinct value, in the order they
// first appear. The shared rule is left out when every match has timeouts,
// because a rule with no matches would match every path.
func buildRouteRules(routes []appsv1.RouteMatch, defaultPathType gatewayv1.PathMatchType, backendRefs []gatewayv1.HTTPBackendRef) []gatewayv1.HTTPRouteRule {
	shared := make([]gatewayv1.HTTPRouteMatch, 0, len(routes))
	var timed []gatewayv1.HTTPRouteRule
	timedIndex := map[appsv1.RouteTimeouts]int{}

	for _, route := range routes {
		match := buildRouteMatch(route, defaultPathType)
		if route.Timeouts == nil {
			shared = append(shared, match)
			continue
		}
		i, ok := timedIndex[*route.Timeouts]
		if !ok {
			i = len(timed)
			timedIndex[*route.Timeouts] = i
			timed = append(timed, gatewayv1.HTTPRouteRule{
				Timeouts:    buildRouteTimeouts(route.Timeouts),
				BackendRefs: append([]gatewayv1.HTTPBackendRef(nil), backendRefs...),
			})
		}
		timed[i].Matches = append(timed[i].Matches, match)
	}

	rules := make([]gatewayv1.HTTPRouteRule, 0, len(timed)+1)
	if len(shared) > 0 || len(timed) == 0 {
		rules = append(rules, gatewayv1.HTTPRouteRule{Matches: shared, BackendRefs: backendRefs})
	}
	return append(rules, timed...)
}

// buildRouteMatch builds the path match for a route, using defaultPathType
// when the route does not set one.
func buildRouteMatch(route appsv1.RouteMatch, defaultPathType gatewayv1.PathMatchType) gatewayv1.HTTPRouteMatch {
	pathType := defaultPathType
	switch route.PathType {
	case "Exact":
		pathType = gatewayv1.PathMatchExact
	case "PathPrefix":
		pathType = gatewayv1.PathMatchPathPrefix
	}
	pathValue := route.PathPrefix
	return gatewayv1.HTTPRouteMatch{
		Path: &gatewayv1.HTTPPathMatch{
			Type:  &pathType,
			Value: &pathValue,
		},
	}
}

// buildRouteTimeouts converts a route's timeouts to Gateway API timeouts.
func buildRouteTimeouts(t *appsv1.RouteTimeouts) *gatewayv1.HTTPRouteTimeouts {
	out := &gatewayv1.HTTPRouteTimeouts{}
	if t.Request != "" {
		d := gatewayv1.Duration(t.Request)
		out.Request = &d
	}
	if t.BackendRequest != "" {
		d := gatewayv1.Duration(t.BackendRequest)
		out.BackendRequest = &d
	}
	return out
}

// buildBackendRefs generates backend references for the HTTPRoute
func (r *RoutingReconciler) buildBackendRefs(nebariApp *appsv1.NebariApp) []gatewayv1.HTTPBackendRef {
	// weight := int32(100)
	// if nebariApp.Spec.Service.Weight != nil {
	// 	weight = *nebariApp.Spec.Service.Weight
	// }

	port := nebariApp.Spec.Service.Port

	// Use specified service namespace, or default to NebariApp's namespace
	serviceNamespace := nebariApp.Spec.Service.Namespace
	if serviceNamespace == "" {
		serviceNamespace = nebariApp.Namespace
	}

	backendRef := gatewayv1.BackendObjectReference{
		Name: gatewayv1.ObjectName(nebariApp.Spec.Service.Name),
		Port: &port,
	}

	// Only set namespace if it's different from the HTTPRoute's namespace
	// to support cross-namespace service references
	if serviceNamespace != nebariApp.Namespace {
		ns := gatewayv1.Namespace(serviceNamespace)
		backendRef.Namespace = &ns
	}

	return []gatewayv1.HTTPBackendRef{
		{
			BackendRef: gatewayv1.BackendRef{
				BackendObjectReference: backendRef,
				// Weight: &weight,
			},
		},
	}
}

// ReconcilePublicRoute creates or updates the public (unauthenticated) HTTPRoute for a NebariApp.
// This route handles paths listed in routing.publicRoutes that should bypass OIDC authentication.
func (r *RoutingReconciler) ReconcilePublicRoute(ctx context.Context, nebariApp *appsv1.NebariApp, tlsListenerName string) error {
	logger := log.FromContext(ctx)

	// Only create public route if there are public routes configured
	if nebariApp.Spec.Routing == nil || len(nebariApp.Spec.Routing.PublicRoutes) == 0 {
		// No public paths - clean up any existing public route
		return r.CleanupPublicHTTPRoute(ctx, nebariApp)
	}

	gatewayName := naming.GatewayName(nebariApp)
	logger.Info("Reconciling public route", "gateway", gatewayName, "hostname", nebariApp.Spec.Hostname,
		"publicRoutes", nebariApp.Spec.Routing.PublicRoutes)

	desiredRoute, err := r.buildPublicHTTPRoute(nebariApp, gatewayName, tlsListenerName)
	if err != nil {
		logger.Error(err, "Failed to build public HTTPRoute")
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeRoutingReady, metav1.ConditionFalse,
			"BuildFailed", fmt.Sprintf("Failed to build public HTTPRoute: %v", err))
		return err
	}

	existingRoute := &gatewayv1.HTTPRoute{}
	routeKey := client.ObjectKey{
		Name:      desiredRoute.Name,
		Namespace: desiredRoute.Namespace,
	}

	err = r.Client.Get(ctx, routeKey, existingRoute)
	if err != nil {
		if errors.IsNotFound(err) {
			if err := r.Client.Create(ctx, desiredRoute); err != nil {
				logger.Error(err, "Failed to create public HTTPRoute")
				return err
			}
			logger.Info("Created public HTTPRoute", "name", desiredRoute.Name)
			r.Recorder.Event(nebariApp, corev1.EventTypeNormal, appsv1.EventReasonHTTPRouteCreated,
				fmt.Sprintf("Created public HTTPRoute %s", desiredRoute.Name))
			return nil
		}
		return err
	}

	// Update existing public HTTPRoute
	existingRoute.Spec = desiredRoute.Spec
	if err := r.Client.Update(ctx, existingRoute); err != nil {
		if errors.IsConflict(err) {
			logger.V(1).Info("Public HTTPRoute update conflict, will retry", "name", existingRoute.Name)
			return nil
		}
		logger.Error(err, "Failed to update public HTTPRoute")
		return err
	}

	logger.Info("Updated public HTTPRoute", "name", existingRoute.Name)
	r.Recorder.Event(nebariApp, corev1.EventTypeNormal, appsv1.EventReasonHTTPRouteUpdated,
		fmt.Sprintf("Updated public HTTPRoute %s", existingRoute.Name))

	return nil
}

// CleanupPublicHTTPRoute removes the public HTTPRoute for a NebariApp
func (r *RoutingReconciler) CleanupPublicHTTPRoute(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	logger := log.FromContext(ctx)

	routeName := naming.PublicHTTPRouteName(nebariApp)
	route := &gatewayv1.HTTPRoute{}
	routeKey := client.ObjectKey{
		Name:      routeName,
		Namespace: nebariApp.Namespace,
	}

	if err := r.Client.Get(ctx, routeKey, route); err != nil {
		if errors.IsNotFound(err) {
			return nil
		}
		return err
	}

	if err := r.Client.Delete(ctx, route); err != nil {
		logger.Error(err, "Failed to delete public HTTPRoute")
		return err
	}

	logger.Info("Deleted public HTTPRoute", "name", routeName)
	r.Recorder.Event(nebariApp, corev1.EventTypeNormal, appsv1.EventReasonHTTPRouteDeleted,
		fmt.Sprintf("Deleted public HTTPRoute %s", routeName))

	return nil
}

// buildPublicHTTPRoute generates an HTTPRoute for public routes that bypass OIDC authentication.
// This route is separate from the main route so the SecurityPolicy only targets the main route.
func (r *RoutingReconciler) buildPublicHTTPRoute(nebariApp *appsv1.NebariApp, gatewayName string, tlsListenerName string) (*gatewayv1.HTTPRoute, error) {
	routeName := naming.PublicHTTPRouteName(nebariApp)
	namespace := gatewayv1.Namespace(constants.GatewayNamespace)

	sectionName := gatewayv1.SectionName("https")
	tlsEnabled := true
	if nebariApp.Spec.Routing != nil && nebariApp.Spec.Routing.TLS != nil && nebariApp.Spec.Routing.TLS.Enabled != nil && !*nebariApp.Spec.Routing.TLS.Enabled {
		sectionName = gatewayv1.SectionName("http")
		tlsEnabled = false
	}
	if tlsListenerName != "" && tlsEnabled {
		sectionName = gatewayv1.SectionName(tlsListenerName)
	}

	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      routeName,
			Namespace: nebariApp.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "nebariapp",
				"app.kubernetes.io/instance":   nebariApp.Name,
				"app.kubernetes.io/managed-by": "nebari-operator",
				"nebari.dev/route-type":        "public",
			},
			Annotations: map[string]string{
				"nebari.dev/tls-enabled": fmt.Sprintf("%t", tlsEnabled),
			},
		},
		Spec: gatewayv1.HTTPRouteSpec{
			CommonRouteSpec: gatewayv1.CommonRouteSpec{
				ParentRefs: []gatewayv1.ParentReference{
					{
						Name:        gatewayv1.ObjectName(gatewayName),
						Namespace:   &namespace,
						SectionName: &sectionName,
					},
				},
			},
			Hostnames: []gatewayv1.Hostname{
				gatewayv1.Hostname(nebariApp.Spec.Hostname),
			},
			// Public routes default to Exact, which is safer for an auth bypass.
			Rules: buildRouteRules(nebariApp.Spec.Routing.PublicRoutes, gatewayv1.PathMatchExact, r.buildBackendRefs(nebariApp)),
		},
	}

	if err := controllerutil.SetControllerReference(nebariApp, route, r.Scheme); err != nil {
		return nil, fmt.Errorf("failed to set controller reference on public HTTPRoute: %w", err)
	}

	return route, nil
}

// validateGateway checks if the specified gateway exists
func (r *RoutingReconciler) validateGateway(ctx context.Context, gatewayName string) error {
	gateway := &gatewayv1.Gateway{}
	gatewayKey := client.ObjectKey{
		Name:      gatewayName,
		Namespace: constants.GatewayNamespace,
	}

	if err := r.Client.Get(ctx, gatewayKey, gateway); err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf("gateway %s not found in namespace %s", gatewayName, constants.GatewayNamespace)
		}
		return fmt.Errorf("failed to get gateway: %w", err)
	}

	return nil
}
