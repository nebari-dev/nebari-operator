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
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"
)

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

	existing := &rbacv1.Role{}
	err := r.Client.Get(ctx, types.NamespacedName{Name: rbacName, Namespace: nebariApp.Namespace}, existing)
	if apierrors.IsNotFound(err) {
		role.Rules = []rbacv1.PolicyRule{
			{
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				ResourceNames: []string{secretName},
				Verbs:         []string{"get"},
			},
		}
		if err := controllerutil.SetControllerReference(nebariApp, role, r.Client.Scheme()); err != nil {
			return fmt.Errorf("failed to set owner reference on Role: %w", err)
		}
		if err := r.Client.Create(ctx, role); err != nil {
			return fmt.Errorf("failed to create Role: %w", err)
		}
		logger.Info("Created OIDC secret reader Role", "name", rbacName, "serviceAccount", saName)
	} else if err != nil {
		return fmt.Errorf("failed to get Role: %w", err)
	} else {
		existing.Rules = []rbacv1.PolicyRule{
			{
				APIGroups:     []string{""},
				Resources:     []string{"secrets"},
				ResourceNames: []string{secretName},
				Verbs:         []string{"get"},
			},
		}
		if err := r.Client.Update(ctx, existing); err != nil {
			return fmt.Errorf("failed to update Role: %w", err)
		}
	}

	// Reconcile RoleBinding
	rb := &rbacv1.RoleBinding{
		ObjectMeta: metav1.ObjectMeta{
			Name:      rbacName,
			Namespace: nebariApp.Namespace,
		},
	}

	existingRB := &rbacv1.RoleBinding{}
	err = r.Client.Get(ctx, types.NamespacedName{Name: rbacName, Namespace: nebariApp.Namespace}, existingRB)
	if apierrors.IsNotFound(err) {
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
		if err := controllerutil.SetControllerReference(nebariApp, rb, r.Client.Scheme()); err != nil {
			return fmt.Errorf("failed to set owner reference on RoleBinding: %w", err)
		}
		if err := r.Client.Create(ctx, rb); err != nil {
			return fmt.Errorf("failed to create RoleBinding: %w", err)
		}
		logger.Info("Created OIDC secret reader RoleBinding", "name", rbacName, "serviceAccount", saName)
	} else if err != nil {
		return fmt.Errorf("failed to get RoleBinding: %w", err)
	} else {
		existingRB.Subjects = []rbacv1.Subject{
			{
				Kind:      "ServiceAccount",
				Name:      saName,
				Namespace: nebariApp.Namespace,
			},
		}
		if err := r.Client.Update(ctx, existingRB); err != nil {
			return fmt.Errorf("failed to update RoleBinding: %w", err)
		}
	}

	return nil
}
