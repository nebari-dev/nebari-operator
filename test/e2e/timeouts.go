//go:build e2e
// +build e2e

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

package e2e

import "time"

// Centralized timeout and polling interval constants for E2E tests.
// Using consistent values across tests improves reliability and makes
// test execution more predictable.
const (
	// ShortTimeout is for quick operations like resource lookups, status checks
	// Example: kubectl get, checking if resource exists
	ShortTimeout = 30 * time.Second

	// MediumTimeout is for operations that involve reconciliation
	// Example: Deployment ready, NebariApp reconciliation, HTTPRoute creation
	MediumTimeout = 2 * time.Minute

	// LongTimeout is for complex scenarios involving multiple resources
	// Example: Full stack setup, authentication flow, multiple resource creation
	LongTimeout = 3 * time.Minute

	// VeryLongTimeout is for slow operations like namespace deletion with finalizers
	VeryLongTimeout = 5 * time.Minute

	// PollInterval is how frequently to poll during Eventually assertions
	// 200ms provides good responsiveness without overwhelming the API server
	PollInterval = 200 * time.Millisecond

	// SlowPollInterval for less critical checks or to reduce API server load
	SlowPollInterval = time.Second
)
