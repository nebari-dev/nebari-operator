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
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

const (
	adminEventsPageSize = 100
	userResourcePrefix  = "users/"
)

// adminEvent is the wire format of a Keycloak admin event. Only the fields
// the operator needs are declared
type adminEvent struct {
	ID             string          `json:"id"`
	Time           int64           `json:"time"`
	AuthDetails    adminEventActor `json:"authDetails"`
	ResourcePath   string          `json:"resourcePath"`
	Representation string          `json:"representation"`
}

// adminEventActor is the authDetails block of an admin event.
type adminEventActor struct {
	ClientID  string `json:"clientId"`
	UserID    string `json:"userId"`
	IPAddress string `json:"ipAddress"`
}

// DeletionActor identifies who performed a deletion in Keycloak.
type DeletionActor struct {
	ClientID  string
	UserID    string
	IPAddress string
}

// UserDeletionEvent is a user deletion read from the Keycloak admin events.
// UserID comes from the event's resource path and is always set. Username
// comes from the event's representation and is empty when Keycloak did not
// record one.
type UserDeletionEvent struct {
	UserID       string
	Username     string
	DeletedAt    time.Time
	DeletedBy    DeletionActor
	AdminEventID string
}

// ListUserDeletions returns the user deletions recorded in the managed realm
// since the given time.
func (c *Client) ListUserDeletions(ctx context.Context, since time.Time) ([]UserDeletionEvent, error) {
	ctx, cancel := c.withTimeout(ctx)
	defer cancel()

	kc, token, err := c.login(ctx)
	if err != nil {
		return nil, err
	}

	url := strings.TrimRight(c.cfg.URL, "/") + "/admin/realms/" + c.cfg.Realm + "/admin-events"

	var events []adminEvent
	for first := 0; ; first += adminEventsPageSize {
		var page []adminEvent
		r, err := kc.GetRequestWithBearerAuth(ctx, token.AccessToken).
			SetQueryParams(map[string]string{
				"resourceTypes":  "USER",
				"operationTypes": "DELETE",
				// dateFrom is day-granular, so query from the start of since's day
				// Unwanted records will be discarded below when mapping each adminEvent
				// to a UserDeletionEvent
				"dateFrom": since.UTC().Format(time.DateOnly),
				"first":    strconv.Itoa(first),
				"max":      strconv.Itoa(adminEventsPageSize),
			}).
			SetResult(&page).
			Get(url)
		if err != nil {
			return nil, fmt.Errorf("failed to list Keycloak admin events: %w", err)
		}
		if r.IsError() {
			return nil, fmt.Errorf("keycloak returned %s listing admin events: %s", r.Status(), r.String())
		}

		events = append(events, page...)
		if len(page) < adminEventsPageSize {
			break
		}
	}

	deletions := make([]UserDeletionEvent, 0, len(events))
	for _, e := range events {
		deletion, err := parseAdminEvent(e)
		if err != nil {
			return nil, err
		}
		// Given that the keycloak API is day-granular, we discard those events recorded
		// on the same day but before the cursor's date
		if deletion.DeletedAt.Before(since) {
			continue
		}
		deletions = append(deletions, deletion)
	}

	return deletions, nil
}

// parseAdminEvent turns a Keycloak API response into a UserDeletionEvent. The user id is
// taken from the resource path, which is always present. The username is taken
// from the representation, which is only present when admin event details are
// enabled on the realm, and is left empty otherwise.
func parseAdminEvent(e adminEvent) (UserDeletionEvent, error) {
	userID, ok := strings.CutPrefix(e.ResourcePath, userResourcePrefix)
	if !ok || userID == "" {
		return UserDeletionEvent{}, fmt.Errorf("admin event %s: unexpected resource path %q", e.ID, e.ResourcePath)
	}

	var username string
	if e.Representation != "" {
		var rep struct {
			Username string `json:"username"`
		}
		if err := json.Unmarshal([]byte(e.Representation), &rep); err != nil {
			return UserDeletionEvent{}, fmt.Errorf("admin event %s: invalid representation: %w", e.ID, err)
		}
		username = rep.Username
	}

	return UserDeletionEvent{
		UserID:    userID,
		Username:  username,
		DeletedAt: time.UnixMilli(e.Time).UTC(),
		DeletedBy: DeletionActor{
			ClientID:  e.AuthDetails.ClientID,
			UserID:    e.AuthDetails.UserID,
			IPAddress: e.AuthDetails.IPAddress,
		},
		AdminEventID: e.ID,
	}, nil
}
