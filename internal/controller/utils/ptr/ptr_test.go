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

package ptr

import "testing"

func TestTo(t *testing.T) {
	s := To("hello")
	if s == nil || *s != "hello" {
		t.Errorf("expected pointer to %q, got %v", "hello", s)
	}

	n := To(42)
	if n == nil || *n != 42 {
		t.Errorf("expected pointer to %d, got %v", 42, n)
	}

	b := To(true)
	if b == nil || *b != true {
		t.Errorf("expected pointer to %v, got %v", true, b)
	}
}
