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

package auth

import (
	"context"
	"fmt"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/naming"
	rbacv1 "k8s.io/api/rbac/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

// reconcileSecretRBAC creates or updates a Role and RoleBinding that scopes
// read access to the OIDC client Secret to the app's ServiceAccount.
func (r *AuthReconciler) reconcileSecretRBAC(ctx context.Context, nebariApp *appsv1.NebariApp) error {
	logger := log.FromContext(ctx)

	secretName := naming.ClientSecretName(nebariApp)
	rbacName := fmt.Sprintf("%s-oidc-secret-reader", nebariApp.Name)

	saName := nebariApp.Spec.ServiceAccountName
	if saName == "" {
		saName = nebariApp.Name
	}

	// Reconcile Role
	role := &rbacv1.Role{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rbacName,
			Namespace: nebariApp.Namespace,
		},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, role, func() error {
		role.Rules = []rbacv1.PolicyRule{
			{
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				ResourceNames: []string{secretName},
				Verbs:         []string{"get"},
			},
		}
		return controllerutil.SetControllerReference(nebariApp, role, r.Client.Scheme())
	}); err != nil {
		return fmt.Errorf("failed to reconcile OIDC secret reader Role: %w", err)
	}

	// Reconcile RoleBinding
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rbacName,
			Namespace: nebariApp.Namespace,
		},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, rb, func() error {
		rb.RoleRef = rbacv1.RoleRef{
			APIGroup: "rbac.authorization.k8s.io",
			Kind:     "Role",
			Name:     rbacName,
		}
		rb.Subjects = []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      saName,
				Namespace: nebariApp.Namespace,
			},
		}
		return controllerutil.SetControllerReference(nebariApp, rb, r.Client.Scheme())
	}); err != nil {
		return fmt.Errorf("failed to reconcile OIDC secret reader RoleBinding: %w", err)
	}

	logger.Info("Reconciled OIDC secret RBAC", "name", rbacName, "serviceAccount", saName)
	return nil
}
