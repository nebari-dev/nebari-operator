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

package controller

import (
	"reflect"
	"testing"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
)

func TestBuildServiceDiscoveryStatus_RequiredGroups(t *testing.T) {
	tests := []struct {
		name   string
		auth   *appsv1.AuthConfig
		want   []string
		wantOK string
	}{
		{name: "bare and trailing-slash names are published as full paths", auth: &appsv1.AuthConfig{Enabled: true, Groups: []string{"finance", "/ops/"}}, want: []string{"/finance", "/ops"}, wantOK: "private"},
		{name: "full paths unchanged", auth: &appsv1.AuthConfig{Enabled: true, Groups: []string{"/a/b"}}, want: []string{"/a/b"}, wantOK: "private"},
		{name: "no groups", auth: &appsv1.AuthConfig{Enabled: true}, want: nil, wantOK: "private"},
		{name: "auth disabled", auth: &appsv1.AuthConfig{Groups: []string{"finance"}}, want: nil, wantOK: "public"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			app := &appsv1.NebariApp{Spec: appsv1.NebariAppSpec{
				Hostname:    "a.example.com",
				Auth:        tt.auth,
				LandingPage: &appsv1.LandingPageConfig{Enabled: true},
			}}
			got := buildServiceDiscoveryStatus(app)
			if len(got.RequiredGroups) != len(tt.want) || (len(tt.want) > 0 && !reflect.DeepEqual(got.RequiredGroups, tt.want)) {
				t.Errorf("RequiredGroups = %v, want %v", got.RequiredGroups, tt.want)
			}
			if got.Visibility != tt.wantOK {
				t.Errorf("Visibility = %q, want %q", got.Visibility, tt.wantOK)
			}
		})
	}
}
