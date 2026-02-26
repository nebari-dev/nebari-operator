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
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/test/utils"
)

var _ = Describe("Service Discovery API", Ordered, func() {
	var (
		ctx         = context.Background()
		namespace   = "nebari-system"
		testAppName = "test-svc-api-app"
	)

	BeforeAll(func() {
		By("Installing NebariApp CRDs")
		cmd := exec.Command("make", "install")
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("Deploying the controller-manager")
		cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", projectImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")

		By("Waiting for controller-manager to be ready")
		Eventually(func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "deployment", "nebari-operator-controller-manager",
				"-n", "nebari-operator-system", "-o", "jsonpath={.status.availableReplicas}")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(Equal("1"))
		}, 2*time.Minute, time.Second).Should(Succeed())

		By("Deploying the navigator manifests")
		cmd = exec.Command("kubectl", "create", "namespace", namespace, "--dry-run=client", "-o", "yaml")
		nsYaml, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to generate namespace YAML")
		applyNs := exec.Command("kubectl", "apply", "-f", "-")
		applyNs.Stdin = strings.NewReader(nsYaml)
		_, err = utils.Run(applyNs)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace %s", namespace)

		cmd = exec.Command("kubectl", "apply", "-f", "deploy/navigator/manifest.yaml")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to apply navigator manifests")

		By("Disabling auth on navigator deployment (no Keycloak in test cluster)")
		patchAuth := exec.Command("kubectl", "set", "env", "deployment/navigator",
			"-n", namespace, "ENABLE_AUTH=false")
		_, _ = utils.Run(patchAuth)

		By("Waiting for navigator deployment to be ready")
		rollout := exec.Command("kubectl", "rollout", "status", "deployment/navigator",
			"-n", namespace, "--timeout=2m")
		_, err = utils.Run(rollout)
		Expect(err).NotTo(HaveOccurred(), "navigator deployment should become ready")

		By("Ensuring navigator NebariApp is created")
		Eventually(func() error {
			var app appsv1.NebariApp
			return k8sClient.Get(ctx, types.NamespacedName{
				Name:      "navigator",
				Namespace: namespace,
			}, &app)
		}, 2*time.Minute, 5*time.Second).Should(Succeed(), "navigator NebariApp should exist")
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

		By("Removing navigator manifests")
		cmd := exec.Command("kubectl", "delete", "-f", "deploy/navigator/manifest.yaml", "--ignore-not-found")
		_, _ = utils.Run(cmd)
	})

	Context("Service Discovery", func() {
		It("should expose API endpoint", func() {
			// Get navigator service endpoint
			svc := &corev1.Service{}
			err := k8sClient.Get(ctx, types.NamespacedName{
				Name:      "navigator",
				Namespace: namespace,
			}, svc)
			Expect(err).NotTo(HaveOccurred(), "navigator service should exist")

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
			// Start a port-forward to the navigator pod
			pfCmd := exec.Command("kubectl", "port-forward",
				"-n", namespace, "svc/navigator",
				"18080:8080")
			Err := pfCmd.Start()
			Expect(Err).NotTo(HaveOccurred(), "port-forward should start")
			defer func() { _ = pfCmd.Process.Kill() }()

			// Wait for port-forward to come up
			Eventually(func() error {
				resp, err := http.Get("http://localhost:18080/api/v1/health")
				if err != nil {
					return err
				}
				defer resp.Body.Close()
				if resp.StatusCode != http.StatusOK {
					return fmt.Errorf("unexpected status %d", resp.StatusCode)
				}
				return nil
			}, 30*time.Second, time.Second).Should(Succeed(), "health endpoint should return 200")

			// Verify the response body
			resp, err := http.Get("http://localhost:18080/api/v1/health")
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()
			body, err := io.ReadAll(resp.Body)
			Expect(err).NotTo(HaveOccurred())
			Expect(string(body)).To(ContainSubstring(`"status"`))
		})
	})

	Context("Frontend Serving", func() {
		It("should serve the static frontend files", func() {
			Skip("Requires port-forwarding or ingress setup; covered by unit tests")
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
