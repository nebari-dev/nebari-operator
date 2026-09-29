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

import (
	"os"
	"testing"
	"time"
)

func TestLoadLifecycleConfig(t *testing.T) {
	tests := []struct {
		name     string
		envVars  map[string]string
		expected LifecycleConfig
	}{
		{
			name:    "Default values",
			envVars: map[string]string{},
			expected: LifecycleConfig{
				PollInterval:             5 * time.Minute,
				GracePeriod:              30 * 24 * time.Hour,
				EventRetention:           7 * 24 * time.Hour,
				CursorConfigMapName:      "user-deletion-cursor",
				CursorConfigMapNamespace: "nebari-operator-system",
			},
		},
		{
			name: "Custom values",
			envVars: map[string]string{
				envLifecyclePollInterval:             "30s",
				envLifecycleGracePeriod:              "168h",
				envLifecycleEventRetention:           "48h",
				envLifecycleCursorConfigMapName:      "cursor",
				envLifecycleCursorConfigMapNamespace: "ops",
			},
			expected: LifecycleConfig{
				PollInterval:             30 * time.Second,
				GracePeriod:              168 * time.Hour,
				EventRetention:           48 * time.Hour,
				CursorConfigMapName:      "cursor",
				CursorConfigMapNamespace: "ops",
			},
		},
		{
			name: "Invalid duration falls back to default",
			envVars: map[string]string{
				envLifecyclePollInterval: "soon",
			},
			expected: LifecycleConfig{
				PollInterval:             5 * time.Minute,
				GracePeriod:              30 * 24 * time.Hour,
				EventRetention:           7 * 24 * time.Hour,
				CursorConfigMapName:      "user-deletion-cursor",
				CursorConfigMapNamespace: "nebari-operator-system",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// getEnv treats an empty value as set, so unset rather than blank.
			// t.Setenv first so the original value is restored after the test.
			for _, key := range []string{
				envLifecyclePollInterval,
				envLifecycleGracePeriod,
				envLifecycleEventRetention,
				envLifecycleCursorConfigMapName,
				envLifecycleCursorConfigMapNamespace,
			} {
				t.Setenv(key, "")
				if err := os.Unsetenv(key); err != nil {
					t.Fatalf("unsetting %s: %v", key, err)
				}
			}
			for k, v := range tt.envVars {
				t.Setenv(k, v)
			}
			if got := LoadLifecycleConfig(); got != tt.expected {
				t.Errorf("expected %+v, got %+v", tt.expected, got)
			}
		})
	}
}
