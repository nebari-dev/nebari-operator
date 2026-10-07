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

package keycloak

import (
	"encoding/json"
	"testing"
	"time"
)

// sampleAdminEvent is the response Keycloak 26.5 returned for a user deletion
// with admin event details enabled, as captured during the design work.
const sampleAdminEvent = `{
  "id": "c067d551-c623-4448-a4aa-0dbf41208d5c",
  "time": 1789648255270,
  "realmId": "b5d87387-1fcc-41e5-8a40-666ab658cf26",
  "authDetails": {
    "realmId": "e98e0e17-a537-4af7-9c0a-85ab61e3158d",
    "clientId": "ebe55ba1-09e2-4835-99aa-fd38e5761f84",
    "userId": "18a5a8cc-f27f-44d7-b3d7-8366b2fca605",
    "ipAddress": "172.18.0.1"
  },
  "operationType": "DELETE",
  "resourceType": "USER",
  "resourcePath": "users/91fb5e10-5017-48ad-8568-0e9210a306cb",
  "representation": "{\"id\":\"91fb5e10-5017-48ad-8568-0e9210a306cb\",\"username\":\"scratch-del-test\"}"
}`

func TestParseAdminEvent(t *testing.T) {
	var sample adminEvent
	if err := json.Unmarshal([]byte(sampleAdminEvent), &sample); err != nil {
		t.Fatalf("decoding sample: %v", err)
	}

	tests := []struct {
		name    string
		mutate  func(e *adminEvent)
		want    UserDeletionEvent
		wantErr bool
	}{
		{
			name:   "full event",
			mutate: func(*adminEvent) {},
			want: UserDeletionEvent{
				UserID:    "91fb5e10-5017-48ad-8568-0e9210a306cb",
				Username:  "scratch-del-test",
				DeletedAt: time.UnixMilli(1789648255270).UTC(),
				DeletedBy: DeletionActor{
					ClientID:  "ebe55ba1-09e2-4835-99aa-fd38e5761f84",
					UserID:    "18a5a8cc-f27f-44d7-b3d7-8366b2fca605",
					IPAddress: "172.18.0.1",
				},
				AdminEventID: "c067d551-c623-4448-a4aa-0dbf41208d5c",
			},
		},
		{
			name:   "missing representation leaves username empty",
			mutate: func(e *adminEvent) { e.Representation = "" },
			want: UserDeletionEvent{
				UserID:    "91fb5e10-5017-48ad-8568-0e9210a306cb",
				DeletedAt: time.UnixMilli(1789648255270).UTC(),
				DeletedBy: DeletionActor{
					ClientID:  "ebe55ba1-09e2-4835-99aa-fd38e5761f84",
					UserID:    "18a5a8cc-f27f-44d7-b3d7-8366b2fca605",
					IPAddress: "172.18.0.1",
				},
				AdminEventID: "c067d551-c623-4448-a4aa-0dbf41208d5c",
			},
		},
		{
			name:    "malformed representation is an error",
			mutate:  func(e *adminEvent) { e.Representation = "{not json" },
			wantErr: true,
		},
		{
			name:    "resource path without users prefix is an error",
			mutate:  func(e *adminEvent) { e.ResourcePath = "groups/abc" },
			wantErr: true,
		},
		{
			name:    "resource path with empty id is an error",
			mutate:  func(e *adminEvent) { e.ResourcePath = "users/" },
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			e := sample
			tt.mutate(&e)

			got, err := parseAdminEvent(e)
			if tt.wantErr {
				if err == nil {
					t.Fatalf("expected error, got %+v", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if got != tt.want {
				t.Errorf("expected %+v, got %+v", tt.want, got)
			}
		})
	}
}

// One event the operator cannot read must not hide the ones after it.
func TestFilterDeletionsSkipsUnparseableEvents(t *testing.T) {
	var sample adminEvent
	if err := json.Unmarshal([]byte(sampleAdminEvent), &sample); err != nil {
		t.Fatalf("decoding sample: %v", err)
	}

	bad := sample
	bad.ID = "bad"
	bad.ResourcePath = "groups/abc"
	good := sample
	good.ID = "good"
	good.Time = sample.Time + 1000

	got := filterDeletions([]adminEvent{bad, good}, time.UnixMilli(sample.Time).UTC())
	if len(got) != 1 || got[0].AdminEventID != "good" {
		t.Fatalf("expected only the readable event, got %+v", got)
	}
}
