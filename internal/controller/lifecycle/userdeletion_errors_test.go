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
	"errors"
	"testing"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation/field"
)

func TestPermanentCreateError(t *testing.T) {
	jobs := schema.GroupResource{Group: "batch", Resource: "jobs"}

	tests := []struct {
		name      string
		err       error
		permanent bool
	}{
		{
			name:      "invalid spec",
			err:       apierrors.NewInvalid(schema.GroupKind{Group: "batch", Kind: "Job"}, "x", field.ErrorList{field.Required(field.NewPath("spec"), "")}),
			permanent: true,
		},
		{
			name:      "bad request",
			err:       apierrors.NewBadRequest("malformed"),
			permanent: true,
		},
		{
			name:      "admission denial",
			err:       apierrors.NewForbidden(jobs, "x", errors.New("denied by policy")),
			permanent: true,
		},
		{
			name:      "quota exhaustion is transient despite being forbidden",
			err:       apierrors.NewForbidden(jobs, "x", errors.New("exceeded quota: jobs, requested: count/jobs.batch=1, used: 5, limited: 5")),
			permanent: false,
		},
		{
			name:      "server timeout",
			err:       apierrors.NewServerTimeout(jobs, "create", 1),
			permanent: false,
		},
		{
			name:      "too many requests",
			err:       apierrors.NewTooManyRequests("slow down", 5),
			permanent: false,
		},
		{
			name:      "conflict",
			err:       apierrors.NewConflict(jobs, "x", errors.New("conflict")),
			permanent: false,
		},
		{
			name:      "non-API error",
			err:       errors.New("dial tcp: connection refused"),
			permanent: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := permanentCreateError(tt.err); got != tt.permanent {
				t.Errorf("permanentCreateError(%v) = %v, want %v", tt.err, got, tt.permanent)
			}
		})
	}
}
