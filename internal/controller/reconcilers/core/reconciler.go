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

package core

import (
	"context"
	"fmt"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/conditions"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// CoreReconciler handles core validation and status management for NebariApp resources.
type CoreReconciler struct {
	Client   client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

func (r *CoreReconciler) ValidateSpec(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	logger := log.FromContext(ctx)

	// Validate namespace is opted-in
	if err := ValidateNamespaceOptIn(ctx, r.Client, nebariApp); err != nil {
		logger.Error(err, "Namespace validation failed")
		// Send event and set condition
		r.Recorder.Event(nebariApp, corev1.EventTypeWarning, appsv1.EventReasonNamespaceNotOptIn, err.Error())
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeReady, metav1.ConditionFalse,
			appsv1.ReasonNamespaceNotOptedIn, err.Error())
		return err
	}

	// Validate referenced service exists and has the specified port
	if err := ValidateService(ctx, r.Client, nebariApp); err != nil {
		logger.Error(err, "Service validation failed")
		// Send event and set condition
		r.Recorder.Event(nebariApp, corev1.EventTypeWarning, appsv1.EventReasonServiceNotFound, err.Error())
		conditions.SetCondition(nebariApp, appsv1.ConditionTypeReady, metav1.ConditionFalse,
			appsv1.ReasonServiceNotFound, err.Error())
		return err
	}

	logger.Info("Core validation passed", "nebariapp", nebariApp.Name)
	r.Recorder.Event(nebariApp, corev1.EventTypeNormal, appsv1.EventReasonValidationSuccess,
		"NebariApp validation completed successfully")

	// Set Ready condition to True since core validation passed
	// Note: This may be overridden by routing reconciliation failures
	conditions.SetCondition(nebariApp, appsv1.ConditionTypeReady, metav1.ConditionTrue,
		appsv1.ReasonValidationSuccess, "Core validation passed")

	return nil
}

const (
	// ManagedNamespaceLabel is the label that indicates a namespace is opted-in to Nebari management
	ManagedNamespaceLabel = "nebari.dev/managed"
)

// ####################################################################
// All validation of the required spec fields for NebariApp resources.
// ####################################################################

// ValidateNamespaceOptIn checks if the namespace has the required label for Nebari management.
// Returns an error if the namespace is not opted-in or cannot be accessed.
func ValidateNamespaceOptIn(ctx context.Context, c client.Client, nebariApp *appsv1.NebariApp) error {
	namespace := &corev1.Namespace{}

	if err := c.Get(ctx, client.ObjectKey{Name: nebariApp.Namespace}, namespace); err != nil {
		return fmt.Errorf("failed to get namespace: %w", err)
	}

	if namespace.Labels == nil || namespace.Labels[ManagedNamespaceLabel] != "true" {
		return fmt.Errorf("namespace %s is not opted-in to Nebari management (missing label: %s=true)",
			nebariApp.Namespace, ManagedNamespaceLabel)
	}

	return nil
}

// ValidateService checks if the referenced service exists in the namespace and has the specified port.
// Returns an error if the service doesn't exist or the port is not exposed.
func ValidateService(ctx context.Context, c client.Client, nebariApp *appsv1.NebariApp) error {
	service := &corev1.Service{}

	serviceKey := client.ObjectKey{
		Name:      nebariApp.Spec.Service.Name,
		Namespace: nebariApp.Namespace,
	}

	if err := c.Get(ctx, serviceKey, service); err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf("service %s not found in namespace %s",
				nebariApp.Spec.Service.Name, nebariApp.Namespace)
		}
		return fmt.Errorf("failed to get service: %w", err)
	}

	// Validate that the specified port exists on the service
	portFound := false
	for _, port := range service.Spec.Ports {
		if port.Port == nebariApp.Spec.Service.Port {
			portFound = true
			break
		}
	}

	if !portFound {
		return fmt.Errorf("service %s does not expose port %d",
			nebariApp.Spec.Service.Name, nebariApp.Spec.Service.Port)
	}

	return nil
}
