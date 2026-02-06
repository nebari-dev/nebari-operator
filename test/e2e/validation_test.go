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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/nebari-dev/nebari-operator/test/utils"
)

// This file contains tests for validation and error handling scenarios.
// These tests ensure the operator properly validates configurations and
// handles invalid inputs gracefully.

var _ = Describe("NebariApp Validation", Ordered, func() {
	const testNamespace = "e2e-test-validation"

	BeforeAll(func() {
		var cmd *exec.Cmd
		var err error

		By("installing NebariApp CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("creating test namespace")
		cmd = exec.Command("kubectl", "create", "namespace", testNamespace)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("labeling namespace for Operator management")
		cmd = exec.Command("kubectl", "label", "namespace", testNamespace, "nebari.dev/managed=true")
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
		}, MediumTimeout, PollInterval).Should(Succeed())
	})

	AfterAll(func() {
		By("cleaning up test resources")
		cmd := exec.Command("kubectl", "delete", "namespace", testNamespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)

		By("undeploying the controller-manager")
		cmd = exec.Command("make", "undeploy")
		_, _ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("make", "uninstall")
		_, _ = utils.Run(cmd)
	})

	Context("Service Reference Validation", func() {
		It("should set Ready=False condition when service does not exist", func() {
			appName := "test-invalid-service"

			By("creating NebariApp referencing non-existent service")
			nebariAppYAML := fmt.Sprintf(`
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: %s
  namespace: %s
spec:
  hostname: invalid-service.example.com
  service:
    name: non-existent-service
    port: 80
  routing:
    tls:
      enabled: false
`, appName, testNamespace)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nebariAppYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create NebariApp")

			By("waiting for Ready condition to be False")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "nebariapp", appName, "-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("False"))
			}, MediumTimeout, PollInterval).Should(Succeed(),
				func() string { return NebariAppDiagnostics(testNamespace, appName) })

			By("verifying condition message mentions service not found")
			cmd = exec.Command("kubectl", "get", "nebariapp", appName, "-n", testNamespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].message}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(ContainSubstring("service"))
			Expect(output).To(ContainSubstring("non-existent-service"))

			By("verifying Ready condition reason is ServiceNotFound")
			cmd = exec.Command("kubectl", "get", "nebariapp", appName, "-n", testNamespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].reason}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(Equal("ServiceNotFound"))

			By("cleaning up")
			cmd = exec.Command("kubectl", "delete", "nebariapp", appName, "-n", testNamespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})
	})

	Context("Hostname Validation", func() {
		It("should reject invalid hostname with uppercase letters", func() {
			appName := "test-invalid-hostname"

			By("attempting to create NebariApp with invalid hostname")
			nebariAppYAML := fmt.Sprintf(`
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: %s
  namespace: %s
spec:
  hostname: Invalid-Hostname.Example.COM
  service:
    name: test-app
    port: 80
`, appName, testNamespace)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nebariAppYAML)
			_, err := utils.Run(cmd)

			By("verifying kubectl rejects the invalid spec")
			Expect(err).To(HaveOccurred(), "kubectl should reject invalid hostname")
		})

		It("should reject hostname starting with hyphen", func() {
			appName := "test-hyphen-hostname"

			By("attempting to create NebariApp with hostname starting with hyphen")
			nebariAppYAML := fmt.Sprintf(`
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: %s
  namespace: %s
spec:
  hostname: -invalid.example.com
  service:
    name: test-app
    port: 80
`, appName, testNamespace)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nebariAppYAML)
			_, err := utils.Run(cmd)

			By("verifying kubectl rejects the invalid spec")
			Expect(err).To(HaveOccurred(), "kubectl should reject hostname starting with hyphen")
		})
	})

	Context("Service Port Validation", func() {
		It("should reject port number out of valid range", func() {
			appName := "test-invalid-port"

			By("attempting to create NebariApp with port > 65535")
			nebariAppYAML := fmt.Sprintf(`
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: %s
  namespace: %s
spec:
  hostname: test-port.example.com
  service:
    name: test-app
    port: 99999
`, appName, testNamespace)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nebariAppYAML)
			_, err := utils.Run(cmd)

			By("verifying kubectl rejects the invalid port")
			Expect(err).To(HaveOccurred(), "kubectl should reject port > 65535")
		})

		It("should reject port number zero", func() {
			appName := "test-zero-port"

			By("attempting to create NebariApp with port 0")
			nebariAppYAML := fmt.Sprintf(`
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: %s
  namespace: %s
spec:
  hostname: test-port-zero.example.com
  service:
    name: test-app
    port: 0
`, appName, testNamespace)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nebariAppYAML)
			_, err := utils.Run(cmd)

			By("verifying kubectl rejects zero port")
			Expect(err).To(HaveOccurred(), "kubectl should reject port 0")
		})
	})

	Context("Path Validation", func() {
		It("should reject path not starting with slash", func() {
			appName := "test-invalid-path"

			By("attempting to create NebariApp with path not starting with /")
			nebariAppYAML := fmt.Sprintf(`
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: %s
  namespace: %s
spec:
  hostname: test-path.example.com
  service:
    name: test-app
    port: 80
  routing:
    tls:
      enabled: false
    routes:
      - pathPrefix: api/v1
        pathType: PathPrefix
`, appName, testNamespace)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nebariAppYAML)
			_, err := utils.Run(cmd)

			By("verifying kubectl rejects the invalid path")
			Expect(err).To(HaveOccurred(), "kubectl should reject path not starting with /")
		})
	})
})
