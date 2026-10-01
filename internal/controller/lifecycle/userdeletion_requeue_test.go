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

package lifecycle

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"

	lifecyclev1alpha1 "github.com/nebari-dev/nebari-operator/api/lifecycle/v1alpha1"
)

func TestNextRequeue(t *testing.T) {
	now := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	in := func(d time.Duration) *metav1.Time {
		t := metav1.NewTime(now.Add(d))
		return &t
	}

	const retention = 90 * 24 * time.Hour

	tests := []struct {
		name        string
		dueAt       *metav1.Time
		completedAt *metav1.Time
		blocked     bool
		want        ctrl.Result
	}{
		{
			name:        "a tombstone waits for its retention",
			dueAt:       in(-30 * 24 * time.Hour),
			completedAt: in(-10 * 24 * time.Hour),
			want:        ctrl.Result{RequeueAfter: 80 * 24 * time.Hour},
		},
		{
			name:        "a blocked create still wins over the tombstone timer",
			dueAt:       in(-time.Hour),
			completedAt: in(-time.Hour),
			blocked:     true,
			want:        ctrl.Result{RequeueAfter: createRetryInterval},
		},
		{
			name:  "before dueAt waits for it",
			dueAt: in(48 * time.Hour),
			want:  ctrl.Result{RequeueAfter: 48 * time.Hour},
		},
		{
			name:    "a blocked create retries soon even before dueAt",
			dueAt:   in(48 * time.Hour),
			blocked: true,
			want:    ctrl.Result{RequeueAfter: createRetryInterval},
		},
		{
			name:  "after dueAt waits for Job events",
			dueAt: in(-time.Hour),
			want:  ctrl.Result{},
		},
		{
			name:  "exactly at dueAt waits for Job events",
			dueAt: in(0),
			want:  ctrl.Result{},
		},
		{
			name:    "a blocked create retries after dueAt too",
			dueAt:   in(-time.Hour),
			blocked: true,
			want:    ctrl.Result{RequeueAfter: createRetryInterval},
		},
		{
			name: "no dueAt waits for events",
			want: ctrl.Result{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			marker := &lifecyclev1alpha1.UserDeletion{}
			marker.Status.DueAt = tt.dueAt
			if tt.completedAt != nil {
				marker.Status.Phase = lifecyclev1alpha1.UserDeletionCompleted
				marker.Status.CompletedAt = tt.completedAt
			}
			if got := nextRequeue(marker, tt.blocked, retention, now); got != tt.want {
				t.Errorf("nextRequeue() = %+v, want %+v", got, tt.want)
			}
		})
	}
}
