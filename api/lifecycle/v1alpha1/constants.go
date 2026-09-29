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

package v1alpha1

// Environment variables the operator injects into every container of a
// cleanup Job. They are the contract a hook's script reads.
const (
	// EnvVarUserID is the Keycloak id of the deleted user, same as the OIDC sub claim.
	EnvVarUserID = "NEBARI_KEYCLOAK_USER_ID"
	// EnvVarUsername is the deleted user's username. Empty when Keycloak did not record it.
	EnvVarUsername = "NEBARI_KEYCLOAK_USERNAME"
	// EnvVarDeletedAt is when the user was deleted in Keycloak, RFC 3339.
	EnvVarDeletedAt = "NEBARI_KEYCLOAK_DELETED_AT"
	// EnvVarStage is the cleanup stage this Job runs for.
	EnvVarStage = "NEBARI_CLEANUP_STAGE"
	// EnvVarDryRun is "true" when the script should log instead of act.
	EnvVarDryRun = "NEBARI_CLEANUP_DRY_RUN"
	// EnvVarUserDeletion is the name of the UserDeletion this Job belongs to, for log correlation.
	EnvVarUserDeletion = "NEBARI_USER_DELETION"
)

// Labels the operator sets on every cleanup Job.
const (
	// LabelUserID is the Keycloak id of the deleted user.
	LabelUserID = "lifecycle.nebari.dev/user-id"
	// LabelHook is the name of the UserCleanupHook the Job was built from.
	LabelHook = "lifecycle.nebari.dev/hook"
	// LabelStage is the cleanup stage the Job runs for.
	LabelStage = "lifecycle.nebari.dev/stage"
)
