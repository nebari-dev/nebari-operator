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

var _ = Describe("NebariApp Routing Schema Variations", Ordered, func() {
	var testNamespace string
	var gatewayIP string

	BeforeAll(func() {
		var err error

		testNamespace = "e2e-test-routing"

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

		By("getting Gateway LoadBalancer IP")
		cmd = exec.Command("kubectl", "get", "svc", "-n", "envoy-gateway-system",
			"-l", "gateway.envoyproxy.io/owning-gateway-name=nebari-gateway",
			"-o", "jsonpath={.items[0].status.loadBalancer.ingress[0].ip}")
		gatewayIP, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to get Gateway IP")
		Expect(gatewayIP).NotTo(BeEmpty(), "Gateway IP is empty")

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
		}, 2*time.Minute, time.Second).Should(Succeed(), func() string {
			// Collect diagnostic information when deployment fails
			diagnostics := "\n=== Deployment Diagnostic Information ===\n"

			// Get deployment details
			cmd := exec.Command("kubectl", "get", "deployment", "test-app", "-n", testNamespace, "-o", "yaml")
			if output, err := utils.Run(cmd); err == nil {
				diagnostics += "\nDeployment YAML:\n" + output + "\n"
			}

			// Get pod status
			cmd = exec.Command("kubectl", "get", "pods", "-n", testNamespace, "-l", "app=test-app")
			if output, err := utils.Run(cmd); err == nil {
				diagnostics += "\nPod Status:\n" + output + "\n"
			}

			// Get pod details
			cmd = exec.Command("kubectl", "describe", "pods", "-n", testNamespace, "-l", "app=test-app")
			if output, err := utils.Run(cmd); err == nil {
				diagnostics += "\nPod Details:\n" + output + "\n"
			}

			// Get events
			cmd = exec.Command("kubectl", "get", "events", "-n", testNamespace, "--sort-by=.lastTimestamp")
			if output, err := utils.Run(cmd); err == nil {
				diagnostics += "\nNamespace Events:\n" + output + "\n"
			}

			return diagnostics
		})
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

	Context("TLS Configuration Variations", func() {
		It("should create HTTPRoute with HTTPS listener when TLS is enabled (default)", func() {
			hostname := fmt.Sprintf("tls-enabled-%d.nebari.local", time.Now().Unix())
			appName := "test-tls-enabled"

			By("creating NebariApp with TLS enabled explicitly")
			nebariAppYAML := fmt.Sprintf(`
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: %s
  namespace: %s
spec:
  hostname: %s
  service:
    name: test-app
    port: 80
  routing:
    tls:
      enabled: true
`, appName, testNamespace, hostname)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nebariAppYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for NebariApp to be ready")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "nebariapp", appName, "-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying HTTPRoute uses HTTPS listener")
			cmd = exec.Command("kubectl", "get", "httproute", fmt.Sprintf("%s-route", appName),
				"-n", testNamespace, "-o", "jsonpath={.spec.parentRefs[0].sectionName}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(Equal("https"))

			By("cleaning up")
			cmd = exec.Command("kubectl", "delete", "nebariapp", appName, "-n", testNamespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})

		It("should create HTTPRoute with HTTP listener when TLS is disabled", func() {
			hostname := fmt.Sprintf("tls-disabled-%d.nebari.local", time.Now().Unix())
			appName := "test-tls-disabled"

			By("creating NebariApp with TLS disabled")
			nebariAppYAML := fmt.Sprintf(`
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: %s
  namespace: %s
spec:
  hostname: %s
  service:
    name: test-app
    port: 80
  routing:
    tls:
      enabled: false
`, appName, testNamespace, hostname)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nebariAppYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for NebariApp to be ready")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "nebariapp", appName, "-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying HTTPRoute uses HTTP listener")
			cmd = exec.Command("kubectl", "get", "httproute", fmt.Sprintf("%s-route", appName),
				"-n", testNamespace, "-o", "jsonpath={.spec.parentRefs[0].sectionName}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(Equal("http"))

			By("cleaning up")
			cmd = exec.Command("kubectl", "delete", "nebariapp", appName, "-n", testNamespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})
	})

	Context("Path-Based Routing Variations", func() {
		It("should create HTTPRoute with single PathPrefix rule", func() {
			hostname := fmt.Sprintf("path-prefix-%d.nebari.local", time.Now().Unix())
			appName := "test-path-prefix"

			By("creating NebariApp with single PathPrefix route")
			nebariAppYAML := fmt.Sprintf(`
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: %s
  namespace: %s
spec:
  hostname: %s
  service:
    name: test-app
    port: 80
  routing:
    tls:
      enabled: false
    routes:
      - pathPrefix: /api
        pathType: PathPrefix
`, appName, testNamespace, hostname)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nebariAppYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for NebariApp to be ready")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "nebariapp", appName, "-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying HTTPRoute has PathPrefix match rule")
			cmd = exec.Command("kubectl", "get", "httproute", fmt.Sprintf("%s-route", appName),
				"-n", testNamespace, "-o", "jsonpath={.spec.rules[0].matches[0].path.type}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(Equal("PathPrefix"))

			By("verifying path value is correct")
			cmd = exec.Command("kubectl", "get", "httproute", fmt.Sprintf("%s-route", appName),
				"-n", testNamespace, "-o", "jsonpath={.spec.rules[0].matches[0].path.value}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(Equal("/api"))

			By("cleaning up")
			cmd = exec.Command("kubectl", "delete", "nebariapp", appName, "-n", testNamespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})

		It("should create HTTPRoute with Exact path match", func() {
			hostname := fmt.Sprintf("exact-path-%d.nebari.local", time.Now().Unix())
			appName := "test-exact-path"

			By("creating NebariApp with Exact path match")
			nebariAppYAML := fmt.Sprintf(`
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: %s
  namespace: %s
spec:
  hostname: %s
  service:
    name: test-app
    port: 80
  routing:
    tls:
      enabled: false
    routes:
      - pathPrefix: /health
        pathType: Exact
`, appName, testNamespace, hostname)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nebariAppYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for NebariApp to be ready")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "nebariapp", appName, "-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying HTTPRoute has Exact match type")
			cmd = exec.Command("kubectl", "get", "httproute", fmt.Sprintf("%s-route", appName),
				"-n", testNamespace, "-o", "jsonpath={.spec.rules[0].matches[0].path.type}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(Equal("Exact"))

			By("verifying path value is correct")
			cmd = exec.Command("kubectl", "get", "httproute", fmt.Sprintf("%s-route", appName),
				"-n", testNamespace, "-o", "jsonpath={.spec.rules[0].matches[0].path.value}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(Equal("/health"))

			By("cleaning up")
			cmd = exec.Command("kubectl", "delete", "nebariapp", appName, "-n", testNamespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})

		It("should create HTTPRoute with multiple path rules", func() {
			hostname := fmt.Sprintf("multi-path-%d.nebari.local", time.Now().Unix())
			appName := "test-multi-path"

			By("creating NebariApp with multiple path rules")
			nebariAppYAML := fmt.Sprintf(`
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: %s
  namespace: %s
spec:
  hostname: %s
  service:
    name: test-app
    port: 80
  routing:
    tls:
      enabled: false
    routes:
      - pathPrefix: /api/v1
        pathType: PathPrefix
      - pathPrefix: /api/v2
        pathType: PathPrefix
      - pathPrefix: /health
        pathType: Exact
`, appName, testNamespace, hostname)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nebariAppYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for NebariApp to be ready")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "nebariapp", appName, "-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying HTTPRoute has multiple match rules")
			cmd = exec.Command("kubectl", "get", "httproute", fmt.Sprintf("%s-route", appName),
				"-n", testNamespace, "-o", "jsonpath={.spec.rules[0].matches}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(ContainSubstring("/api/v1"))
			Expect(output).To(ContainSubstring("/api/v2"))
			Expect(output).To(ContainSubstring("/health"))

			By("verifying first path is PathPrefix type")
			cmd = exec.Command("kubectl", "get", "httproute", fmt.Sprintf("%s-route", appName),
				"-n", testNamespace, "-o", "jsonpath={.spec.rules[0].matches[0].path.type}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(Equal("PathPrefix"))

			By("verifying last path is Exact type")
			cmd = exec.Command("kubectl", "get", "httproute", fmt.Sprintf("%s-route", appName),
				"-n", testNamespace, "-o", "jsonpath={.spec.rules[0].matches[2].path.type}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(Equal("Exact"))

			By("cleaning up")
			cmd = exec.Command("kubectl", "delete", "nebariapp", appName, "-n", testNamespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})

		It("should create HTTPRoute with root path /", func() {
			hostname := fmt.Sprintf("root-path-%d.nebari.local", time.Now().Unix())
			appName := "test-root-path"

			By("creating NebariApp with root path")
			nebariAppYAML := fmt.Sprintf(`
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: %s
  namespace: %s
spec:
  hostname: %s
  service:
    name: test-app
    port: 80
  routing:
    tls:
      enabled: false
    routes:
      - pathPrefix: /
        pathType: PathPrefix
`, appName, testNamespace, hostname)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nebariAppYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for NebariApp to be ready")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "nebariapp", appName, "-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying HTTPRoute has root path")
			cmd = exec.Command("kubectl", "get", "httproute", fmt.Sprintf("%s-route", appName),
				"-n", testNamespace, "-o", "jsonpath={.spec.rules[0].matches[0].path.value}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(Equal("/"))

			By("cleaning up")
			cmd = exec.Command("kubectl", "delete", "nebariapp", appName, "-n", testNamespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})
	})

	Context("Combined TLS and Path-Based Routing", func() {
		It("should create HTTPRoute with HTTPS listener and path rules", func() {
			hostname := fmt.Sprintf("tls-path-%d.nebari.local", time.Now().Unix())
			appName := "test-tls-path"

			By("creating NebariApp with TLS enabled and path rules")
			nebariAppYAML := fmt.Sprintf(`
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: %s
  namespace: %s
spec:
  hostname: %s
  service:
    name: test-app
    port: 80
  routing:
    tls:
      enabled: true
    routes:
      - pathPrefix: /api
        pathType: PathPrefix
      - pathPrefix: /app
        pathType: PathPrefix
`, appName, testNamespace, hostname)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nebariAppYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for NebariApp to be ready")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "nebariapp", appName, "-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying HTTPRoute uses HTTPS listener")
			cmd = exec.Command("kubectl", "get", "httproute", fmt.Sprintf("%s-route", appName),
				"-n", testNamespace, "-o", "jsonpath={.spec.parentRefs[0].sectionName}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(Equal("https"))

			By("verifying HTTPRoute has path rules")
			cmd = exec.Command("kubectl", "get", "httproute", fmt.Sprintf("%s-route", appName),
				"-n", testNamespace, "-o", "jsonpath={.spec.rules[0].matches}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(ContainSubstring("/api"))
			Expect(output).To(ContainSubstring("/app"))

			By("cleaning up")
			cmd = exec.Command("kubectl", "delete", "nebariapp", appName, "-n", testNamespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})

		It("should create HTTPRoute with HTTP listener and multiple paths", func() {
			hostname := fmt.Sprintf("http-multi-path-%d.nebari.local", time.Now().Unix())
			appName := "test-http-multi-path"

			By("creating NebariApp with TLS disabled and multiple path rules")
			nebariAppYAML := fmt.Sprintf(`
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: %s
  namespace: %s
spec:
  hostname: %s
  service:
    name: test-app
    port: 80
  routing:
    tls:
      enabled: false
    routes:
      - pathPrefix: /api/v1
        pathType: PathPrefix
      - pathPrefix: /api/v2
        pathType: PathPrefix
      - pathPrefix: /status
        pathType: Exact
`, appName, testNamespace, hostname)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nebariAppYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for NebariApp to be ready")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "nebariapp", appName, "-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying HTTPRoute uses HTTP listener")
			cmd = exec.Command("kubectl", "get", "httproute", fmt.Sprintf("%s-route", appName),
				"-n", testNamespace, "-o", "jsonpath={.spec.parentRefs[0].sectionName}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(Equal("http"))

			By("verifying HTTPRoute has all path rules")
			cmd = exec.Command("kubectl", "get", "httproute", fmt.Sprintf("%s-route", appName),
				"-n", testNamespace, "-o", "json")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(ContainSubstring("/api/v1"))
			Expect(output).To(ContainSubstring("/api/v2"))
			Expect(output).To(ContainSubstring("/status"))

			By("cleaning up")
			cmd = exec.Command("kubectl", "delete", "nebariapp", appName, "-n", testNamespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})
	})

	Context("No Routing Configuration", func() {
		It("should handle NebariApp without routing section (defaults)", func() {
			hostname := fmt.Sprintf("no-routing-%d.nebari.local", time.Now().Unix())
			appName := "test-no-routing"

			By("creating NebariApp without routing configuration")
			nebariAppYAML := fmt.Sprintf(`
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: %s
  namespace: %s
spec:
  hostname: %s
  service:
    name: test-app
    port: 80
`, appName, testNamespace, hostname)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nebariAppYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for NebariApp to be processed")
			time.Sleep(10 * time.Second)

			By("verifying RoutingReady condition is False")
			cmd = exec.Command("kubectl", "get", "nebariapp", appName, "-n", testNamespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='RoutingReady')].status}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(Equal("False"))

			By("verifying HTTPRoute was not created")
			cmd = exec.Command("kubectl", "get", "httproute", fmt.Sprintf("%s-route", appName), "-n", testNamespace)
			_, err = utils.Run(cmd)
			Expect(err).To(HaveOccurred(), "HTTPRoute should not exist without routing config")

			By("cleaning up")
			cmd = exec.Command("kubectl", "delete", "nebariapp", appName, "-n", testNamespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})

		It("should create HTTPRoute when only TLS is specified (no paths)", func() {
			hostname := fmt.Sprintf("tls-only-%d.nebari.local", time.Now().Unix())
			appName := "test-tls-only"

			By("creating NebariApp with only TLS configuration")
			nebariAppYAML := fmt.Sprintf(`
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: %s
  namespace: %s
spec:
  hostname: %s
  service:
    name: test-app
    port: 80
  routing:
    tls:
      enabled: true
`, appName, testNamespace, hostname)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nebariAppYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for NebariApp to be ready")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "nebariapp", appName, "-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying HTTPRoute was created")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "httproute", fmt.Sprintf("%s-route", appName), "-n", testNamespace)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
			}, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying HTTPRoute uses HTTPS listener")
			cmd = exec.Command("kubectl", "get", "httproute", fmt.Sprintf("%s-route", appName),
				"-n", testNamespace, "-o", "jsonpath={.spec.parentRefs[0].sectionName}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(Equal("https"))

			By("verifying HTTPRoute has default path match (Gateway API adds / when matches is empty)")
			cmd = exec.Command("kubectl", "get", "httproute", fmt.Sprintf("%s-route", appName),
				"-n", testNamespace, "-o", "jsonpath={.spec.rules[0].matches[0].path.value}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			// Gateway API automatically adds "/" path when matches array is empty
			Expect(output).To(Equal("/"))

			By("cleaning up")
			cmd = exec.Command("kubectl", "delete", "nebariapp", appName, "-n", testNamespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})
	})

	Context("Hostname Variations", func() {
		It("should handle different hostname formats", func() {
			testCases := []struct {
				name     string
				hostname string
			}{
				{"simple subdomain", fmt.Sprintf("app-%d.nebari.local", time.Now().Unix())},
				{"multi-level subdomain", fmt.Sprintf("app.sub-%d.nebari.local", time.Now().Unix())},
				{"hyphenated name", fmt.Sprintf("my-app-%d.nebari.local", time.Now().Unix())},
			}

			for _, tc := range testCases {
				By(fmt.Sprintf("testing hostname format: %s", tc.name))
				appName := fmt.Sprintf("test-hostname-%d", time.Now().UnixNano())

				nebariAppYAML := fmt.Sprintf(`
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: %s
  namespace: %s
spec:
  hostname: %s
  service:
    name: test-app
    port: 80
  routing:
    tls:
      enabled: false
`, appName, testNamespace, tc.hostname)

				cmd := exec.Command("kubectl", "apply", "-f", "-")
				cmd.Stdin = strings.NewReader(nebariAppYAML)
				_, err := utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred())

				By(fmt.Sprintf("waiting for NebariApp %s to be ready", appName))
				Eventually(func(g Gomega) {
					cmd := exec.Command("kubectl", "get", "nebariapp", appName, "-n", testNamespace,
						"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
					output, err := utils.Run(cmd)
					g.Expect(err).NotTo(HaveOccurred())
					g.Expect(output).To(Equal("True"))
				}, 3*time.Minute, 5*time.Second).Should(Succeed())

				By(fmt.Sprintf("verifying HTTPRoute hostname matches: %s", tc.hostname))
				cmd = exec.Command("kubectl", "get", "httproute", fmt.Sprintf("%s-route", appName),
					"-n", testNamespace, "-o", "jsonpath={.spec.hostnames[0]}")
				output, err := utils.Run(cmd)
				Expect(err).NotTo(HaveOccurred())
				Expect(output).To(Equal(tc.hostname))

				By("cleaning up")
				cmd = exec.Command("kubectl", "delete", "nebariapp", appName, "-n", testNamespace, "--ignore-not-found")
				_, _ = utils.Run(cmd)

				// Small delay between tests to avoid conflicts
				time.Sleep(2 * time.Second)
			}
		})
	})
})
