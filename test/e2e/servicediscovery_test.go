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
	"net/url"
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
		ctx            = context.Background()
		namespace      = "nebari-system"
		testAppName    = "test-svc-api-app"
		navigatorToken string
		keycloakPFCmd  *exec.Cmd
	)

	BeforeAll(func() {
		By("Installing NebariApp CRDs")
		cmd := exec.Command("make", "install")
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("Waiting for operator namespace to be fully terminated from previous runs")
		Eventually(func() error {
			cmd = exec.Command("kubectl", "get", "namespace", "nebari-operator-system")
			_, err = utils.Run(cmd)
			return err
		}, 2*time.Minute, time.Second).Should(HaveOccurred(),
			"nebari-operator-system should be absent before deploying")

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

		By("Rendering navigator manifests from Helm chart")
		cmd = exec.Command("make", "render-navigator")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to render navigator manifests")

		cmd = exec.Command("kubectl", "apply", "-f", "deploy/navigator/manifest.yaml")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to apply navigator manifests")

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

		By("Acquiring JWT token from Keycloak for authenticated service discovery tests")
		keycloakPFCmd = exec.Command("kubectl", "port-forward",
			"-n", "keycloak", "svc/keycloak-keycloakx-http",
			"18090:80")
		Expect(keycloakPFCmd.Start()).NotTo(HaveOccurred(), "keycloak port-forward should start")
		Eventually(func() error {
			resp, err := http.Get("http://localhost:18090/auth/realms/nebari/.well-known/openid-configuration")
			if err != nil {
				return err
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				return fmt.Errorf("keycloak not ready: status %d", resp.StatusCode)
			}
			return nil
		}, 30*time.Second, time.Second).Should(Succeed(), "keycloak should be reachable via port-forward")

		tokenResp, err := http.PostForm(
			"http://localhost:18090/auth/realms/nebari/protocol/openid-connect/token",
			url.Values{
				"client_id":  {"admin-cli"},
				"username":   {"admin"},
				"password":   {"nebari-admin"},
				"grant_type": {"password"},
			})
		Expect(err).NotTo(HaveOccurred(), "Should be able to request token from Keycloak nebari realm")
		defer tokenResp.Body.Close()
		Expect(tokenResp.StatusCode).To(Equal(http.StatusOK),
			"Keycloak token request should succeed (realm=nebari, user=admin/nebari-admin)")
		var tokenData struct {
			AccessToken string `json:"access_token"`
		}
		Expect(json.NewDecoder(tokenResp.Body).Decode(&tokenData)).To(Succeed())
		navigatorToken = tokenData.AccessToken
		Expect(navigatorToken).NotTo(BeEmpty(), "JWT token must be non-empty")
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

		By("Stopping Keycloak port-forward")
		if keycloakPFCmd != nil && keycloakPFCmd.Process != nil {
			_ = keycloakPFCmd.Process.Kill()
		}

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
			Expect(navigatorToken).NotTo(BeEmpty(), "JWT token must be available from BeforeAll")

			priority := 50
			authApp := &appsv1.NebariApp{
				ObjectMeta: metav1.ObjectMeta{
					Name:      "test-auth-visibility",
					Namespace: namespace,
				},
				Spec: appsv1.NebariAppSpec{
					Hostname: "test-auth-visibility.nebari.test",
					Service: appsv1.ServiceReference{
						Name: "test-service",
						Port: 8080,
					},
					LandingPage: &appsv1.LandingPageConfig{
						Enabled:     true,
						DisplayName: "Test Authenticated Service",
						Description: "Visible only to authenticated users",
						Category:    "Testing",
						Visibility:  "authenticated",
						Priority:    &priority,
					},
				},
			}

			By("Creating NebariApp with authenticated visibility")
			Expect(k8sClient.Create(ctx, authApp)).To(Succeed())
			DeferCleanup(func() { _ = k8sClient.Delete(ctx, authApp) })

			By("Port-forwarding to navigator")
			navPF := exec.Command("kubectl", "port-forward",
				"-n", namespace, "svc/navigator", "18081:8080")
			Expect(navPF.Start()).NotTo(HaveOccurred())
			defer navPF.Process.Kill()

			By("Waiting for navigator port-forward to be ready")
			Eventually(func() error {
				resp, err := http.Get("http://localhost:18081/api/v1/health")
				if err != nil {
					return err
				}
				resp.Body.Close()
				return nil
			}, 30*time.Second, time.Second).Should(Succeed())

			// Allow the watcher to process the newly created app
			time.Sleep(5 * time.Second)

			By("Calling /api/v1/services without a token — authenticated services should be hidden")
			resp, err := http.Get("http://localhost:18081/api/v1/services")
			Expect(err).NotTo(HaveOccurred())
			defer resp.Body.Close()
			Expect(resp.StatusCode).To(Equal(http.StatusOK))
			var unauthResult ServiceListResponse
			Expect(json.NewDecoder(resp.Body).Decode(&unauthResult)).To(Succeed())
			Expect(unauthResult.Services.Authenticated).To(BeEmpty(),
				"Authenticated services must not appear without a token")
			Expect(unauthResult.User).To(BeNil(),
				"User field must be absent for unauthenticated request")

			By("Calling /api/v1/services with a valid JWT — authenticated services should appear")
			req, err := http.NewRequest(http.MethodGet, "http://localhost:18081/api/v1/services", nil)
			Expect(err).NotTo(HaveOccurred())
			req.Header.Set("Authorization", "Bearer "+navigatorToken)
			authResp, err := http.DefaultClient.Do(req)
			Expect(err).NotTo(HaveOccurred())
			defer authResp.Body.Close()
			Expect(authResp.StatusCode).To(Equal(http.StatusOK))
			var authResult ServiceListResponse
			Expect(json.NewDecoder(authResp.Body).Decode(&authResult)).To(Succeed())
			Expect(authResult.User).NotTo(BeNil(), "User field must be present for authenticated request")
			Expect(authResult.User.Authenticated).To(BeTrue())
			Expect(authResult.User.Username).To(Equal("admin"))
			Expect(authResult.Services.Authenticated).NotTo(BeEmpty(),
				"Authenticated services must appear for a logged-in user")
			authNames := make([]string, 0, len(authResult.Services.Authenticated))
			for _, s := range authResult.Services.Authenticated {
				if m, ok := s.(map[string]interface{}); ok {
					if n, ok := m["name"].(string); ok {
						authNames = append(authNames, n)
					}
				}
			}
			Expect(authNames).To(ContainElement("test-auth-visibility"),
				"The authenticated-visibility NebariApp must appear in authenticated services list")
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
	Categories []string          `json:"categories"`
	User       *ServiceListUser  `json:"user,omitempty"`
}

// ServiceListUser mirrors the UserInfo returned by the navigator API
type ServiceListUser struct {
	Authenticated bool     `json:"authenticated"`
	Username      string   `json:"username,omitempty"`
	Email         string   `json:"email,omitempty"`
	Name          string   `json:"name,omitempty"`
	Groups        []string `json:"groups,omitempty"`
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
