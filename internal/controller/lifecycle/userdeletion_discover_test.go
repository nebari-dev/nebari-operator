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

	lifecyclev1alpha1 "github.com/nebari-dev/nebari-operator/api/lifecycle/v1alpha1"
)

// discoverHooks must tell a hook that is gone from one that exists but may
// not run right now. Only the first one skips the entry.
func TestDiscoverHooksKeepsPendingEntryForPresentHook(t *testing.T) {
	hook := *testHook("dsp-delete", "ns", lifecyclev1alpha1.CleanupStageDelete)
	marker := testMarker("u1")

	// First pass: the hook is eligible and gets an entry.
	discoverHooks(marker, []lifecyclev1alpha1.UserCleanupHook{hook}, []lifecyclev1alpha1.UserCleanupHook{hook})
	if len(marker.Status.Hooks) != 1 || marker.Status.Hooks[0].State != lifecyclev1alpha1.HookPending {
		t.Fatalf("after first pass: %+v", marker.Status.Hooks)
	}

	// Second pass: the hook still exists but is no longer eligible.
	discoverHooks(marker, []lifecyclev1alpha1.UserCleanupHook{hook}, nil)
	if got := marker.Status.Hooks[0].State; got != lifecyclev1alpha1.HookPending {
		t.Errorf("present but ineligible hook: got %q, want Pending", got)
	}

	// Third pass: the hook is gone.
	discoverHooks(marker, nil, nil)
	entry := marker.Status.Hooks[0]
	if entry.State != lifecyclev1alpha1.HookSkipped || entry.Reason != lifecyclev1alpha1.HookReasonHookRemoved {
		t.Errorf("removed hook: got %q/%q, want Skipped/%s", entry.State, entry.Reason, lifecyclev1alpha1.HookReasonHookRemoved)
	}
}

func TestDiscoverHooksOnlyRecordsEligibleHooks(t *testing.T) {
	hook := *testHook("dsp-delete", "ns", lifecyclev1alpha1.CleanupStageDelete)
	marker := testMarker("u1")

	discoverHooks(marker, []lifecyclev1alpha1.UserCleanupHook{hook}, nil)
	if len(marker.Status.Hooks) != 0 {
		t.Errorf("ineligible hook got an entry: %+v", marker.Status.Hooks)
	}
}
