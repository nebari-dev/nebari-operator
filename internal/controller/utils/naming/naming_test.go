/*
Copyright 2025.

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

package naming

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	appsv1 "github.com/nebari-dev/nic-operator/api/v1"
)

func TestResourceName(t *testing.T) {
	nicApp := &appsv1.NicApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-app",
			Namespace: "default",
		},
	}

	result := ResourceName(nicApp, "route")
	expected := "my-app-route"
	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}
}

func TestHTTPRouteName(t *testing.T) {
	nicApp := &appsv1.NicApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-app",
			Namespace: "default",
		},
	}

	result := HTTPRouteName(nicApp)
	expected := "my-app-route"
	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}
}

func TestCertificateName(t *testing.T) {
	nicApp := &appsv1.NicApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-app",
			Namespace: "default",
		},
	}

	result := CertificateName(nicApp)
	expected := "my-app-cert"
	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}
}

func TestClientID(t *testing.T) {
	nicApp := &appsv1.NicApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-app",
			Namespace: "production",
		},
	}

	result := ClientID(nicApp)
	expected := "my-app-production-client"
	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}
}

func TestClientSecretName(t *testing.T) {
	nicApp := &appsv1.NicApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-app",
			Namespace: "default",
		},
	}

	result := ClientSecretName(nicApp)
	expected := "my-app-oidc-client"
	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}
}

func TestSecurityPolicyName(t *testing.T) {
	nicApp := &appsv1.NicApp{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "my-app",
			Namespace: "default",
		},
	}

	result := SecurityPolicyName(nicApp)
	expected := "my-app-security"
	if result != expected {
		t.Errorf("expected %s, got %s", expected, result)
	}
}
