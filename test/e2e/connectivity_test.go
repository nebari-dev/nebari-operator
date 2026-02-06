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
	"fmt"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/nebari-dev/nebari-operator/test/utils"
)

// ConnectivityDiagnostics gathers debugging information for connectivity failures
func ConnectivityDiagnostics(namespace, appName string) string {
	var output strings.Builder
	output.WriteString("\n=== Connectivity Diagnostics ===\n")

	// Get NebariApp status
	cmd := exec.Command("kubectl", "get", "nebariapp", appName, "-n", namespace, "-o", "yaml")
	if out, err := utils.Run(cmd); err == nil {
		output.WriteString("\n--- NebariApp ---\n")
		output.WriteString(out)
	}

	// Get HTTPRoute
	cmd = exec.Command("kubectl", "get", "httproute", "-n", namespace, "-o", "yaml")
	if out, err := utils.Run(cmd); err == nil {
		output.WriteString("\n--- HTTPRoutes ---\n")
		output.WriteString(out)
	}

	// Get Service
	cmd = exec.Command("kubectl", "get", "svc", "-n", namespace, "-o", "yaml")
	if out, err := utils.Run(cmd); err == nil {
		output.WriteString("\n--- Services ---\n")
		output.WriteString(out)
	}

	// Get Pods
	cmd = exec.Command("kubectl", "get", "pods", "-n", namespace, "-o", "wide")
	if out, err := utils.Run(cmd); err == nil {
		output.WriteString("\n--- Pods ---\n")
		output.WriteString(out)
	}

	// Get Gateway status
	cmd = exec.Command("kubectl", "get", "gateway", "nebari-gateway", "-n", "envoy-gateway-system", "-o", "yaml")
	if out, err := utils.Run(cmd); err == nil {
		output.WriteString("\n--- Gateway ---\n")
		output.WriteString(out)
	}

	// Get controller logs
	cmd = exec.Command("kubectl", "logs", "-n", "nebari-operator-system", "-l", "control-plane=controller-manager", "--tail=50")
	if out, err := utils.Run(cmd); err == nil {
		output.WriteString("\n--- Controller Logs (last 50 lines) ---\n")
		output.WriteString(out)
	}

	return output.String()
}

var _ = Describe("HTTPRoute Connectivity", Ordered, func() {
	var testNamespace string
	var gatewayIP string

	BeforeAll(func() {
		var err error

		testNamespace = "e2e-test-connectivity"

		By("installing NebariApp CRDs")
		cmd := exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("checking if Gateway exists")
		cmd = exec.Command("kubectl", "get", "gateway", "nebari-gateway", "-n", "envoy-gateway-system")
		_, err = utils.Run(cmd)
		if err != nil {
			Skip("Gateway 'nebari-gateway' not found - run 'make setup' in dev/ first")
		}

		By("waiting for Gateway LoadBalancer IP")
		Eventually(func() string {
			cmd := exec.Command("kubectl", "get", "svc", "-n", "envoy-gateway-system",
				"-l", "gateway.envoyproxy.io/owning-gateway-name=nebari-gateway",
				"-o", "jsonpath={.items[0].status.loadBalancer.ingress[0].ip}")
			output, err := utils.Run(cmd)
			if err != nil {
				return ""
			}
			gatewayIP = strings.TrimSpace(output)
			return gatewayIP
		}, 3*time.Minute, 5*time.Second).ShouldNot(BeEmpty(), "Gateway LoadBalancer IP not assigned")

		By(fmt.Sprintf("Gateway IP: %s", gatewayIP))

		By("creating test namespace")
		cmd = exec.Command("kubectl", "create", "namespace", testNamespace, "--dry-run=client", "-o", "yaml")
		output, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		cmd = exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(output)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("labeling namespace for Operator management")
		cmd = exec.Command("kubectl", "label", "namespace", testNamespace, "nebari.dev/managed=true", "--overwrite")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace")

		By("deploying the controller-manager")
		cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", projectImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")

		By("waiting for controller-manager to be ready")
		Eventually(func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "deployment", "nebari-operator-controller-manager",
				"-n", "nebari-operator-system", "-o", "jsonpath={.status.availableReplicas}")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(Equal("1"))
		}, 2*time.Minute, time.Second).Should(Succeed())

		By("creating a test application deployment")
		appYAML, err := utils.LoadTestDataFile("test-app.yaml", map[string]string{
			"NAMESPACE_PLACEHOLDER": testNamespace,
		})
		Expect(err).NotTo(HaveOccurred(), "Failed to load test-app.yaml")

		cmd = exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(appYAML)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create test application")

		By("waiting for test application to be ready")
		Eventually(func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "deployment", "test-app", "-n", testNamespace,
				"-o", "jsonpath={.status.availableReplicas}")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(Equal("1"))
		}, 2*time.Minute, time.Second).Should(Succeed())
	})

	AfterAll(func() {
		By("cleaning up test resources")
		cmd := exec.Command("kubectl", "delete", "namespace", testNamespace, "--ignore-not-found", "--timeout=60s")
		_, _ = utils.Run(cmd)

		By("undeploying the controller-manager")
		cmd = exec.Command("make", "undeploy")
		_, _ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("make", "uninstall")
		_, _ = utils.Run(cmd)
	})

	Context("HTTP Connectivity", func() {
		var hostname string

		BeforeEach(func() {
			hostname = fmt.Sprintf("test-connectivity-%d.nebari.local", time.Now().Unix())
		})

		It("should be able to reach the app via HTTP when TLS is disabled", func() {
			By("creating a NebariApp with TLS disabled")
			nebariAppYAML := fmt.Sprintf(`
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: test-http-connectivity
  namespace: %s
spec:
  hostname: %s
  service:
    name: test-app
    port: 80
  routing:
    tls:
      enabled: false
`, testNamespace, hostname)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nebariAppYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create NebariApp")

			By("waiting for NebariApp to be ready")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "nebariapp", "test-http-connectivity",
					"-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("waiting for HTTPRoute to be accepted")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "httproute", "test-http-connectivity-route",
					"-n", testNamespace,
					"-o", "jsonpath={.status.parents[0].conditions[?(@.type=='Accepted')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("waiting for HTTPRoute to be programmed (ResolvedRefs)")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "httproute", "test-http-connectivity-route",
					"-n", testNamespace,
					"-o", "jsonpath={.status.parents[0].conditions[?(@.type=='ResolvedRefs')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("dumping HTTPRoute details for debugging")
			cmd = exec.Command("kubectl", "get", "httproute", "test-http-connectivity-route",
				"-n", testNamespace, "-o", "yaml")
			routeYAML, _ := utils.Run(cmd)
			fmt.Fprintf(GinkgoWriter, "HTTPRoute:\n%s\n", routeYAML)

			By("waiting for Envoy configuration to propagate")
			time.Sleep(10 * time.Second)

			By("testing HTTP connectivity via Gateway IP from inside cluster")
			// First, verify routing works inside the cluster
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "run", "curl-test-verify", "--image=curlimages/curl:latest",
					"-i", "--rm", "--restart=Never", "--namespace", testNamespace,
					"--", "curl", "-s", "-o", "/dev/null", "-w", "%{http_code}",
					"--connect-timeout", "5", "--max-time", "10",
					"-H", fmt.Sprintf("Host: %s", hostname),
					fmt.Sprintf("http://%s/", gatewayIP))
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("200"), "Expected HTTP 200 response from inside cluster")
			}, 2*time.Minute, 10*time.Second).Should(Succeed())

			By("testing HTTP connectivity from host (with port-forward fallback)")
			// Try direct access first, fall back to port-forward if needed
			var testURL string
			var portForwardCmd *exec.Cmd

			// Test if LoadBalancer IP is directly accessible from host
			cmd = exec.Command("curl", "-s", "-o", "/dev/null", "-w", "%{http_code}",
				"--connect-timeout", "2", "--max-time", "3",
				"-H", fmt.Sprintf("Host: %s", hostname),
				fmt.Sprintf("http://%s/", gatewayIP))
			if output, err := utils.Run(cmd); err == nil && output == "200" {
				// Direct access works
				testURL = fmt.Sprintf("http://%s/", gatewayIP)
				fmt.Fprintf(GinkgoWriter, "LoadBalancer IP is directly accessible from host\n")
			} else {
				// Need port-forward (typical on macOS)
				fmt.Fprintf(GinkgoWriter, "LoadBalancer IP not accessible from host, using port-forward\n")

				// Get Gateway service name dynamically
				cmd = exec.Command("kubectl", "get", "svc", "-n", "envoy-gateway-system",
					"-l", "gateway.envoyproxy.io/owning-gateway-name=nebari-gateway",
					"-o", "jsonpath={.items[0].metadata.name}")
				gatewaySvcName, err := utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred(), "Failed to get Gateway service name")

				// Start port-forward in background
				portForwardCmd = exec.Command("kubectl", "port-forward",
					"-n", "envoy-gateway-system",
					fmt.Sprintf("svc/%s", gatewaySvcName),
					"8888:80") // Forward to local port 8888

				err = portForwardCmd.Start()
				Expect(err).NotTo(HaveOccurred(), "Failed to start port-forward")

				// Give port-forward time to establish
				time.Sleep(3 * time.Second)

				testURL = "http://localhost:8888/"
			}

			// Ensure cleanup of port-forward
			defer func() {
				if portForwardCmd != nil && portForwardCmd.Process != nil {
					_ = portForwardCmd.Process.Kill()
				}
			}()

			Eventually(func(g Gomega) {
				cmd := exec.Command("curl", "-s", "-o", "/dev/null", "-w", "%{http_code}",
					"--connect-timeout", "10", "--max-time", "15",
					"-H", fmt.Sprintf("Host: %s", hostname), testURL)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("200"), "Expected HTTP 200 response from host")
			}, 1*time.Minute, 5*time.Second).Should(Succeed(),
				func() string { return ConnectivityDiagnostics(testNamespace, "test-http-connectivity") })

			By("verifying response body contains expected content")
			cmd = exec.Command("curl", "-s",
				"--connect-timeout", "10",
				"--max-time", "15",
				"-H", fmt.Sprintf("Host: %s", hostname), testURL)
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(ContainSubstring("Welcome"), "Response should contain app content")

			By("cleaning up NebariApp")
			cmd = exec.Command("kubectl", "delete", "nebariapp", "test-http-connectivity",
				"-n", testNamespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})

		It("should be able to reach the app via HTTPS when TLS is enabled", func() {
			By("creating a NebariApp with TLS enabled")
			nebariAppYAML := fmt.Sprintf(`
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: test-https-connectivity
  namespace: %s
spec:
  hostname: %s
  service:
    name: test-app
    port: 80
  routing:
    tls:
      enabled: true
`, testNamespace, hostname)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nebariAppYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create NebariApp")

			By("waiting for NebariApp to be ready")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "nebariapp", "test-https-connectivity",
					"-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("waiting for HTTPRoute to be accepted")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "httproute", "test-https-connectivity-route",
					"-n", testNamespace,
					"-o", "jsonpath={.status.parents[0].conditions[?(@.type=='Accepted')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("testing HTTPS connectivity via Gateway IP")
			// Try direct access first, fall back to port-forward if needed
			var httpsTestURL string
			var httpsPortForwardCmd *exec.Cmd
			var useHTTPSPortForward bool

			// Test if LoadBalancer IP is directly accessible from host
			cmd = exec.Command("curl", "-s", "-o", "/dev/null", "-w", "%{http_code}",
				"--connect-timeout", "2", "--max-time", "3", "-k",
				"--resolve", fmt.Sprintf("%s:443:%s", hostname, gatewayIP),
				fmt.Sprintf("https://%s/", hostname))
			if output, err := utils.Run(cmd); err == nil && output == "200" {
				// Direct access works
				httpsTestURL = fmt.Sprintf("https://%s/", hostname)
				fmt.Fprintf(GinkgoWriter, "LoadBalancer IP is directly accessible for HTTPS from host\n")
			} else {
				// Need port-forward (typical on macOS)
				fmt.Fprintf(GinkgoWriter, "LoadBalancer IP not accessible for HTTPS from host, using port-forward\n")
				useHTTPSPortForward = true

				// Get Gateway service name dynamically
				cmd = exec.Command("kubectl", "get", "svc", "-n", "envoy-gateway-system",
					"-l", "gateway.envoyproxy.io/owning-gateway-name=nebari-gateway",
					"-o", "jsonpath={.items[0].metadata.name}")
				gatewaySvcName, err := utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred(), "Failed to get Gateway service name")

				// Start port-forward in background for HTTPS
				httpsPortForwardCmd = exec.Command("kubectl", "port-forward",
					"-n", "envoy-gateway-system",
					fmt.Sprintf("svc/%s", gatewaySvcName),
					"8443:443") // Forward to local port 8443

				err = httpsPortForwardCmd.Start()
				Expect(err).NotTo(HaveOccurred(), "Failed to start HTTPS port-forward")

				// Give port-forward time to establish
				time.Sleep(3 * time.Second)

				httpsTestURL = "https://localhost:8443/"
			}

			// Ensure cleanup of port-forward
			defer func() {
				if httpsPortForwardCmd != nil && httpsPortForwardCmd.Process != nil {
					_ = httpsPortForwardCmd.Process.Kill()
				}
			}()

			// Using IP with Host header and SNI, accepting self-signed cert
			Eventually(func(g Gomega) {
				var testCmd *exec.Cmd
				if useHTTPSPortForward {
					testCmd = exec.Command("curl", "-s", "-o", "/dev/null", "-w", "%{http_code}",
						"--connect-timeout", "10", "--max-time", "15",
						"-k", "-H", fmt.Sprintf("Host: %s", hostname), httpsTestURL)
				} else {
					testCmd = exec.Command("curl", "-s", "-o", "/dev/null", "-w", "%{http_code}",
						"--connect-timeout", "10", "--max-time", "15",
						"-k", "--resolve", fmt.Sprintf("%s:443:%s", hostname, gatewayIP), httpsTestURL)
				}
				output, err := utils.Run(testCmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("200"), "Expected HTTP 200 response")
			}, 3*time.Minute, 10*time.Second).Should(Succeed())

			By("verifying response body contains expected content")
			var bodyCmd *exec.Cmd
			if useHTTPSPortForward {
				bodyCmd = exec.Command("curl", "-s",
					"--connect-timeout", "10", "--max-time", "15",
					"-k", "-H", fmt.Sprintf("Host: %s", hostname), httpsTestURL)
			} else {
				bodyCmd = exec.Command("curl", "-s",
					"--connect-timeout", "10", "--max-time", "15",
					"-k", "--resolve", fmt.Sprintf("%s:443:%s", hostname, gatewayIP),
					fmt.Sprintf("https://%s/", hostname))
			}
			output, err := utils.Run(bodyCmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(ContainSubstring("Welcome"), "Response should contain app content")

			By("cleaning up NebariApp")
			cmd = exec.Command("kubectl", "delete", "nebariapp", "test-https-connectivity",
				"-n", testNamespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})
	})
})
