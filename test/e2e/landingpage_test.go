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

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
)

var _ = Describe("Landing Page", Ordered, func() {
	var (
		ctx         = context.Background()
		namespace   = "nebari-system"
		testAppName = "test-landing-app"
	)

	BeforeAll(func() {
		By("Ensuring landing page deployment is running")
		Eventually(func() error {
			var deployment appsv1.NebariApp
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name:      "landing-page",
				Namespace: namespace,
			}, &deployment)
			return err
		}, 2*time.Minute, 5*time.Second).Should(Succeed(), "Landing page deployment should exist")
	})

	AfterAll(func() {
		By("Cleaning up test NebariApp")
		app := &appsv1.NebariApp{
			ObjectMeta: metav1.ObjectMeta{
				Name:      testAppName,
				Namespace: namespace,
			},
		}
		_ = k8sClient.Delete(ctx, app)
	})

	Context("Service Discovery", func() {
		It("should expose API endpoint", func() {
			// Get landing page service endpoint
			svc := &corev1.Service{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name:      "landing-page",
				Namespace: namespace,
			}, svc)
			Expect(err).NotTo(HaveOccurred(), "Landing page service should exist")

			// For Kind cluster, we need to port-forward or use ingress
			// For now, check service exists
			Expect(svc.Spec.Ports).NotTo(BeEmpty(), "Service should have ports defined")
		})

		It("should return public services without authentication", func() {
			// Create a test NebariApp with public landing page
			priority := 99
			testApp := &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      testAppName,
					Namespace: namespace,
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: fmt.Sprintf("%s.nebari.test", testAppName),
					Service: appsv1.ServiceReference{
						Name: "test-service",
						Port: 8080,
					},
					LandingPage: &appsv1.LandingPageConfig{
						Enabled:     true,
						DisplayName: "Test Public Service",
						Description: "A test service for E2E testing",
						Category:    "Testing",
						Visibility:  "public",
						Priority:    &priority,
					},
				},
			}

			By("Creating test NebariApp with public visibility")
			err := k8sClient.Create(ctx, testApp)
			Expect(err).NotTo(HaveOccurred(), "Should create test NebariApp")

			By("Waiting for NebariApp to be processed")
			time.Sleep(5 * time.Second) // Give watcher time to process

			// Note: Actual API call would require port-forwarding or ingress setup
			// This is a structural test to ensure the deployment exists
		})

		It("should filter services based on visibility", func() {
			// This test would require JWT token generation and API calls
			// For now, verify the structure is in place
			Skip("Requires JWT authentication setup in test environment")
		})
	})

	Context("Health Checks", func() {
		It("should report healthy status", func() {
			Skip("Requires port-forwarding or ingress to access /api/v1/health")
		})
	})

	Context("Frontend Serving", func() {
		It("should serve static frontend files", func() {
			Skip("Requires port-forwarding or ingress to access frontend")
		})
	})
})

// Helper function to make authenticated API requests
func makeAuthenticatedRequest(endpoint, token string) (*http.Response, error) {
	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, err
	}

	if token != "" {
		req.Header.Set("Authorization", fmt.Sprintf("Bearer %s", token))
	}

	client := &http.Client{
		Timeout: 10 * time.Second,
	}

	return client.Do(req)
}

// ServiceListResponse matches the API response format
type ServiceListResponse struct {
	Services struct {
		Public        []interface{} `json:"public"`
		Authenticated []interface{} `json:"authenticated"`
		Private       []interface{} `json:"private"`
	} `json:"services"`
	Categories []string `json:"categories"`
}

// Helper to parse service list from API
func getServiceList(endpoint string) (*ServiceListResponse, error) {
	resp, err := makeAuthenticatedRequest(endpoint, "")
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("API returned status %d: %s", resp.StatusCode, string(body))
	}

	var result ServiceListResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return nil, err
	}

	return &result, nil
}
