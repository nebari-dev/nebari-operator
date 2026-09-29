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

package config

import "time"

// Environment variables read by LoadLifecycleConfig.
const (
	envLifecyclePollInterval             = "LIFECYCLE_POLL_INTERVAL"
	envLifecycleGracePeriod              = "LIFECYCLE_GRACE_PERIOD"
	envLifecycleEventRetention           = "LIFECYCLE_EVENT_RETENTION"
	envLifecycleCursorConfigMapName      = "LIFECYCLE_CURSOR_CONFIGMAP_NAME"
	envLifecycleCursorConfigMapNamespace = "LIFECYCLE_CURSOR_CONFIGMAP_NAMESPACE"
)

// LifecycleConfig holds the user cleanup settings for the operator. These are
// cluster policy rather than per-hook settings, so they live on the operator
// deployment and not on the UserCleanupHook CRD.
type LifecycleConfig struct {
	// PollInterval is how often the poller asks Keycloak for user deletions.
	// The "disable" stage of a hook runs within one interval of the deletion.
	PollInterval time.Duration

	// GracePeriod is how long after a Keycloak deletion the "delete" stage runs.
	// It is cluster-wide so that a restore during the window is all or nothing.
	GracePeriod time.Duration

	// EventRetention is how long Keycloak keeps admin events. When the poller
	// has no cursor, on first run or after the cursor ConfigMap was deleted, it
	// asks for deletions this far back so nothing Keycloak still has is missed.
	// It must not exceed the realm's adminEventsExpiration, which NIC sets to
	// seven days. Markers must outlive this window plus the grace period so a
	// replay is dropped as AlreadyExists rather than run twice.
	EventRetention time.Duration

	// CursorConfigMapName is the ConfigMap the poller uses to remember the last
	// admin event it processed, so a restart does not replay old deletions.
	CursorConfigMapName string

	// CursorConfigMapNamespace is where the cursor ConfigMap lives. Should be the
	// operator's own namespace.
	CursorConfigMapNamespace string
}

// LoadLifecycleConfig loads user cleanup configuration from environment variables.
func LoadLifecycleConfig() LifecycleConfig {
	return LifecycleConfig{
		PollInterval:             getEnvDuration(envLifecyclePollInterval, 5*time.Minute),
		GracePeriod:              getEnvDuration(envLifecycleGracePeriod, 30*24*time.Hour),
		EventRetention:           getEnvDuration(envLifecycleEventRetention, 7*24*time.Hour),
		CursorConfigMapName:      getEnv(envLifecycleCursorConfigMapName, "user-deletion-cursor"),
		CursorConfigMapNamespace: getEnv(envLifecycleCursorConfigMapNamespace, "nebari-operator-system"),
	}
}
