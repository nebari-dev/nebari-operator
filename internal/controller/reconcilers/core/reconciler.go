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

package core

import (
	"context"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/record"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	appsv1 "github.com/nebari-dev/nic-operator/api/v1"
	"github.com/nebari-dev/nic-operator/internal/controller/utils/conditions"
	"github.com/nebari-dev/nic-operator/internal/controller/utils/constants"
	"github.com/nebari-dev/nic-operator/internal/controller/utils/validation"
)

// CoreReconciler handles core validation and status management for NicApp resources
type CoreReconciler struct {
	Client   client.Client
	Scheme   *runtime.Scheme
	Recorder record.EventRecorder
}

// ValidateNicApp performs core validation on a NicApp resource
func (r *CoreReconciler) ValidateNicApp(ctx context.Context, nicApp *appsv1.NicApp) error {
	logger := log.FromContext(ctx)

	// Validate namespace is opted-in
	if err := validation.ValidateNamespaceOptIn(ctx, r.Client, nicApp); err != nil {
		logger.Error(err, "Namespace validation failed")
		r.Recorder.Event(nicApp, corev1.EventTypeWarning, constants.EventReasonNamespaceNotOptIn, err.Error())
		conditions.SetCondition(nicApp, appsv1.ConditionTypeReady, metav1.ConditionFalse,
			appsv1.ReasonNamespaceNotOptedIn, err.Error())
		return err
	}

	// Validate service exists
	if err := validation.ValidateService(ctx, r.Client, nicApp); err != nil {
		logger.Error(err, "Service validation failed")
		r.Recorder.Event(nicApp, corev1.EventTypeWarning, constants.EventReasonServiceNotFound, err.Error())
		conditions.SetCondition(nicApp, appsv1.ConditionTypeReady, metav1.ConditionFalse,
			constants.EventReasonServiceNotFound, err.Error())
		return err
	}

	logger.Info("Core validation passed", "nicapp", nicApp.Name)
	r.Recorder.Event(nicApp, corev1.EventTypeNormal, constants.EventReasonValidationSuccess,
		"NicApp validation completed successfully")

	return nil
}
