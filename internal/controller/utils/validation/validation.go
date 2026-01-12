/*
Copyright 2025.

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

package validation

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/errors"
	"sigs.k8s.io/controller-runtime/pkg/client"

	appsv1 "github.com/nebari-dev/nic-operator/api/v1"
)

const (
	// ManagedNamespaceLabel is the label that indicates a namespace is opted-in to NIC management
	ManagedNamespaceLabel = "nic.nebari.dev/managed"
)

// ValidateNamespaceOptIn checks if the namespace has the required label for NIC management.
// Returns an error if the namespace is not opted-in or cannot be accessed.
func ValidateNamespaceOptIn(ctx context.Context, c client.Client, nicApp *appsv1.NicApp) error {
	namespace := &corev1.Namespace{}
	if err := c.Get(ctx, client.ObjectKey{Name: nicApp.Namespace}, namespace); err != nil {
		return fmt.Errorf("failed to get namespace: %w", err)
	}

	if namespace.Labels == nil || namespace.Labels[ManagedNamespaceLabel] != "true" {
		return fmt.Errorf("namespace %s is not opted-in to NIC management (missing label: %s=true)",
			nicApp.Namespace, ManagedNamespaceLabel)
	}

	return nil
}

// ValidateService checks if the referenced service exists in the namespace and has the specified port.
// Returns an error if the service doesn't exist or the port is not exposed.
func ValidateService(ctx context.Context, c client.Client, nicApp *appsv1.NicApp) error {
	service := &corev1.Service{}
	serviceKey := client.ObjectKey{
		Name:      nicApp.Spec.Service.Name,
		Namespace: nicApp.Namespace,
	}

	if err := c.Get(ctx, serviceKey, service); err != nil {
		if errors.IsNotFound(err) {
			return fmt.Errorf("service %s not found in namespace %s",
				nicApp.Spec.Service.Name, nicApp.Namespace)
		}
		return fmt.Errorf("failed to get service: %w", err)
	}

	// Validate that the specified port exists on the service
	portFound := false
	for _, port := range service.Spec.Ports {
		if port.Port == nicApp.Spec.Service.Port {
			portFound = true
			break
		}
	}

	if !portFound {
		return fmt.Errorf("service %s does not expose port %d",
			nicApp.Spec.Service.Name, nicApp.Spec.Service.Port)
	}

	return nil
}
