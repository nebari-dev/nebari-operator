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

// Package groups holds the canonical form of a NebariApp group-path entry.
// It lives outside any OIDC provider so that spec.auth.groups and
// status.serviceDiscovery.requiredGroups agree on what counts as the same
// group, regardless of which provider does the sync.
package groups

import "strings"

// NormalizePath converts a group entry to the full path form used across the
// operator: a leading slash, no trailing slash, and no empty segments
// ("/a//b" becomes "/a/b"). A bare name such as "team-example" becomes
// "/team-example". Returns "" for an empty or whitespace-only entry.
func NormalizePath(group string) string {
	var segments []string
	for _, segment := range strings.Split(strings.TrimSpace(group), "/") {
		if segment != "" {
			segments = append(segments, segment)
		}
	}
	if len(segments) == 0 {
		return ""
	}
	return "/" + strings.Join(segments, "/")
}
