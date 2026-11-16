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

package groups

import "testing"

func TestNormalizePath(t *testing.T) {
	tests := []struct {
		name  string
		entry string
		want  string
	}{
		{"empty", "", ""},
		{"whitespace only", "   ", ""},
		{"just a slash", "/", ""},
		{"double slash", "//", ""},
		{"bare name", "team-example", "/team-example"},
		{"bare name with whitespace", "  team-example  ", "/team-example"},
		{"already a path", "/team-example", "/team-example"},
		{"nested path", "/parent/child", "/parent/child"},
		{"bare nested entry", "parent/child", "/parent/child"},
		{"trailing slash", "/team-example/", "/team-example"},
		{"repeated slashes collapse", "/parent//child", "/parent/child"},
		{"leading double slash", "//team-example", "/team-example"},
		{"multiple interior slashes", "/a///b////c", "/a/b/c"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizePath(tt.entry); got != tt.want {
				t.Errorf("NormalizePath(%q) = %q, want %q", tt.entry, got, tt.want)
			}
		})
	}
}
