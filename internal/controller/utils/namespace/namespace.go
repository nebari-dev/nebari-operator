/*
Copyright (c) 2026, OpenTeams

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

package namespace

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
)

// ManagedNamespaceLabel is the label that opts a namespace in to Nebari management.
const ManagedNamespaceLabel = "nebari.dev/managed"

// IsManaged reports whether the namespace carries ManagedNamespaceLabel=true.
// An error is only returned when the namespace could not be read.
func IsManaged(ctx context.Context, c client.Client, name string) (bool, error) {
	ns := &corev1.Namespace{}
	if err := c.Get(ctx, client.ObjectKey{Name: name}, ns); err != nil {
		return false, fmt.Errorf("failed to get namespace %s: %w", name, err)
	}
	return ns.Labels[ManagedNamespaceLabel] == "true", nil
}
