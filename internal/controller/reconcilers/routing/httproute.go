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

// ReconcileRouting creates or updates the HTTPRoute for a NebariApp
func (r *RoutingReconciler) ReconcileRouting(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	logger := log.FromContext(ctx)

	// Determine which gateway to use
	gatewayName := r.getGatewayName(nebariApp)
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
	desiredRoute := r.buildHTTPRoute(nebariApp, gatewayName)

	// Check if HTTPRoute already exists
	existingRoute := &gatewayv1.HTTPRoute{}
	routeKey := client.ObjectKey{
		Name:      desiredRoute.Name,
		Namespace: desiredRoute.Namespace,
	}

	err := r.Client.Get(ctx, routeKey, existingRoute)
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

// buildHTTPRoute generates an HTTPRoute resource from NebariApp spec
func (r *RoutingReconciler) buildHTTPRoute(nebariApp *appsv1.NebariApp, gatewayName string) *gatewayv1.HTTPRoute {
	routeName := naming.HTTPRouteName(nebariApp)
	namespace := gatewayv1.Namespace(constants.GatewayNamespace)

	// Determine which Gateway listener to use based on TLS configuration
	// Default is HTTPS (TLS enabled) when TLS is not specified or when enabled is nil/true
	sectionName := gatewayv1.SectionName("https")
	tlsEnabled := true
	if nebariApp.Spec.Routing != nil && nebariApp.Spec.Routing.TLS != nil && nebariApp.Spec.Routing.TLS.Enabled != nil && !*nebariApp.Spec.Routing.TLS.Enabled {
		sectionName = gatewayv1.SectionName("http")
		tlsEnabled = false
	}

	route := &gatewayv1.HTTPRoute{
		ObjectMeta: metav1.ObjectMeta{
			Name:      routeName,
			Namespace: nebariApp.Namespace,
			Labels: map[string]string{
				"app.kubernetes.io/name":       "nebariapp",
				"app.kubernetes.io/instance":   nebariApp.Name,
				"app.kubernetes.io/managed-by": "nebari-operator",
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
			Rules: r.buildHTTPRouteRules(nebariApp),
		},
	}

	// Set owner reference for garbage collection
	_ = controllerutil.SetControllerReference(nebariApp, route, r.Scheme)

	return route
}

// buildHTTPRouteRules generates HTTPRoute rules based on NebariApp routes
func (r *RoutingReconciler) buildHTTPRouteRules(nebariApp *appsv1.NebariApp) []gatewayv1.HTTPRouteRule {
	// Get routes from routing config if specified
	var routes []appsv1.RouteMatch
	if nebariApp.Spec.Routing != nil {
		routes = nebariApp.Spec.Routing.Routes
	}

	// Build a single rule with multiple matches (one per route)
	// All matches route to the same backend, so we use one rule
	// If no routes specified, we create an empty matches array. Gateway API will automatically
	// add a default path match of "/" (PathPrefix) when matches is empty or null.
	matches := make([]gatewayv1.HTTPRouteMatch, 0, len(routes))
	for _, route := range routes {
		pathType := gatewayv1.PathMatchPathPrefix
		if route.PathType == "Exact" {
			pathType = gatewayv1.PathMatchExact
		}

		pathValue := route.PathPrefix
		match := gatewayv1.HTTPRouteMatch{
			Path: &gatewayv1.HTTPPathMatch{
				Type:  &pathType,
				Value: &pathValue,
			},
		}
		matches = append(matches, match)
	}

	return []gatewayv1.HTTPRouteRule{
		{
			Matches:     matches,
			BackendRefs: r.buildBackendRefs(nebariApp),
		},
	}
}

// buildBackendRefs generates backend references for the HTTPRoute
func (r *RoutingReconciler) buildBackendRefs(nebariApp *appsv1.NebariApp) []gatewayv1.HTTPBackendRef {
	// weight := int32(100)
	// if nebariApp.Spec.Service.Weight != nil {
	// 	weight = *nebariApp.Spec.Service.Weight
	// }

	port := nebariApp.Spec.Service.Port

	return []gatewayv1.HTTPBackendRef{
		{
			BackendRef: gatewayv1.BackendRef{
				BackendObjectReference: gatewayv1.BackendObjectReference{
					Name: gatewayv1.ObjectName(nebariApp.Spec.Service.Name),
					Port: &port,
				},
				// Weight: &weight,
			},
		},
	}
}

// getGatewayName returns the gateway name based on NebariApp spec
func (r *RoutingReconciler) getGatewayName(nebariApp *appsv1.NebariApp) string {
	if nebariApp.Spec.Gateway == "internal" {
		return constants.InternalGatewayName
	}
	return constants.PublicGatewayName
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
