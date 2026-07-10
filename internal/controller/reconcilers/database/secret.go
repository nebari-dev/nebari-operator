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

package database

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/internal/controller/utils/naming"
)

// credentialKeys maps the normalized key exposed to packs to the key in the
// Secret CloudNativePG generates for the application user.
var credentialKeys = map[string]string{
	"host":     "host",
	"port":     "port",
	"username": "username",
	"password": "password",
	"database": "dbname",
	"uri":      "uri",
}

// reconcileCredentialsSecret copies CNPG's generated app-user Secret into the
// documented "<name>-db-credentials" contract. Returns requeue=true when the
// source Secret does not exist yet (CNPG creates it shortly after the
// Cluster), which is a provisioning state rather than an error.
func (r *DatabaseReconciler) reconcileCredentialsSecret(ctx context.Context, nebariApp *appsv1.NebariApp) (requeue bool, err error) {
	source := &corev1.Secret{}
	if err := r.Client.Get(ctx, types.NamespacedName{
		Name:      naming.DatabaseAppSecretName(nebariApp),
		Namespace: nebariApp.Namespace,
	}, source); err != nil {
		if apierrors.IsNotFound(err) {
			return true, nil
		}
		return false, fmt.Errorf("failed to read CNPG connection secret: %w", err)
	}

	data, err := normalizeCredentials(source.Data)
	if err != nil {
		return false, err
	}

	secret := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{
			Name:      naming.DatabaseSecretName(nebariApp),
			Namespace: nebariApp.Namespace,
		},
	}
	if _, err := controllerutil.CreateOrUpdate(ctx, r.Client, secret, func() error {
		secret.Type = corev1.SecretTypeOpaque
		secret.Data = data
		return controllerReference(nebariApp, secret, r.Scheme)
	}); err != nil {
		return false, fmt.Errorf("failed to reconcile database credentials secret: %w", err)
	}
	return false, nil
}

// normalizeCredentials maps CNPG source keys to the documented contract keys.
func normalizeCredentials(source map[string][]byte) (map[string][]byte, error) {
	data := make(map[string][]byte, len(credentialKeys))
	for target, from := range credentialKeys {
		v, ok := source[from]
		if !ok {
			return nil, fmt.Errorf("CNPG connection secret is missing key %q", from)
		}
		data[target] = v
	}
	return data, nil
}
