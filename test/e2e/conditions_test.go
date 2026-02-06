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
	"encoding/json"
	"fmt"
	"os/exec"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/nebari-dev/nebari-operator/test/utils"
)

// This file tests status condition management and transitions in the NebariApp lifecycle
var _ = Describe("NebariApp Status Conditions", Ordered, func() {
	const testNamespace = "e2e-test-conditions"

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

		By("creating test service")
		serviceYAML := fmt.Sprintf(`
apiVersion: v1
kind: Service
metadata:
  name: test-service
  namespace: %s
spec:
  selector:
    app: test
  ports:
  - port: 8080
    targetPort: 8080
`, testNamespace)
		cmd = exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(serviceYAML)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
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

	Context("Condition State Machine", func() {
		It("should transition Ready condition from Unknown to True", func() {
			appName := "test-ready-transition"

			By("creating NebariApp with minimal config")
			nebariAppYAML := fmt.Sprintf(`
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: %s
  namespace: %s
spec:
  hostname: ready-test.example.com
  service:
    name: test-service
    port: 8080
`, appName, testNamespace)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nebariAppYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying Ready condition eventually becomes True")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "nebariapp", appName, "-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}, MediumTimeout, PollInterval).Should(Succeed(),
				func() string { return NebariAppDiagnostics(testNamespace, appName) })

			By("verifying lastTransitionTime is set")
			cmd = exec.Command("kubectl", "get", "nebariapp", appName, "-n", testNamespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].lastTransitionTime}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).NotTo(BeEmpty(), "lastTransitionTime should be set")

			By("verifying observedGeneration matches metadata.generation")
			cmd = exec.Command("kubectl", "get", "nebariapp", appName, "-n", testNamespace, "-o", "json")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			var app map[string]interface{}
			err = json.Unmarshal([]byte(output), &app)
			Expect(err).NotTo(HaveOccurred())

			metadata := app["metadata"].(map[string]interface{})
			status := app["status"].(map[string]interface{})
			generation := metadata["generation"].(float64)
			observedGeneration := status["observedGeneration"].(float64)
			Expect(observedGeneration).To(Equal(generation))

			By("cleaning up")
			cmd = exec.Command("kubectl", "delete", "nebariapp", appName, "-n", testNamespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})

		It("should maintain lastTransitionTime when condition status doesn't change", func() {
			appName := "test-transition-time"

			By("creating NebariApp")
			nebariAppYAML := fmt.Sprintf(`
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: %s
  namespace: %s
spec:
  hostname: transition-test.example.com
  service:
    name: test-service
    port: 8080
  routing:
    tls:
      enabled: false
`, appName, testNamespace)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nebariAppYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for Ready condition to be True")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "nebariapp", appName, "-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}, MediumTimeout, PollInterval).Should(Succeed())

			By("capturing initial lastTransitionTime")
			cmd = exec.Command("kubectl", "get", "nebariapp", appName, "-n", testNamespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].lastTransitionTime}")
			initialTime, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(initialTime).NotTo(BeEmpty())

			By("triggering reconciliation with annotation update")
			cmd = exec.Command("kubectl", "annotate", "nebariapp", appName, "-n", testNamespace,
				"test-annotation=trigger-reconcile", "--overwrite")
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for reconciliation to complete")
			time.Sleep(5 * time.Second)

			By("verifying lastTransitionTime hasn't changed")
			cmd = exec.Command("kubectl", "get", "nebariapp", appName, "-n", testNamespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].lastTransitionTime}")
			currentTime, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(currentTime).To(Equal(initialTime), "lastTransitionTime should not change when status stays the same")

			By("cleaning up")
			cmd = exec.Command("kubectl", "delete", "nebariapp", appName, "-n", testNamespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})

		It("should have helpful messages in condition reasons", func() {
			appName := "test-condition-messages"

			By("creating NebariApp without routing config")
			nebariAppYAML := fmt.Sprintf(`
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: %s
  namespace: %s
spec:
  hostname: messages-test.example.com
  service:
    name: test-service
    port: 8080
`, appName, testNamespace)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nebariAppYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for reconciliation")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "nebariapp", appName, "-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='RoutingReady')]}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).NotTo(BeEmpty())
			}, MediumTimeout, PollInterval).Should(Succeed())

			By("verifying RoutingReady has descriptive reason")
			cmd = exec.Command("kubectl", "get", "nebariapp", appName, "-n", testNamespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='RoutingReady')].reason}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(Equal("RoutingNotConfigured"), "Reason should be descriptive")

			By("verifying RoutingReady has helpful message")
			cmd = exec.Command("kubectl", "get", "nebariapp", appName, "-n", testNamespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='RoutingReady')].message}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(strings.ToLower(output)).To(ContainSubstring("routing"), "Message should explain why routing is not ready")

			By("cleaning up")
			cmd = exec.Command("kubectl", "delete", "nebariapp", appName, "-n", testNamespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})
	})

	Context("Condition Types", func() {
		It("should set all expected condition types", func() {
			appName := "test-all-conditions"

			By("creating NebariApp with routing enabled")
			tlsEnabled := false
			nebariAppYAML := fmt.Sprintf(`
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: %s
  namespace: %s
spec:
  hostname: all-conditions.example.com
  service:
    name: test-service
    port: 8080
  routing:
    tls:
      enabled: %t
`, appName, testNamespace, tlsEnabled)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nebariAppYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("waiting for reconciliation")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "nebariapp", appName, "-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Or(Equal("True"), Equal("False")))
			}, MediumTimeout, PollInterval).Should(Succeed())

			By("verifying Ready condition exists")
			cmd = exec.Command("kubectl", "get", "nebariapp", appName, "-n", testNamespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='Ready')]}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).NotTo(BeEmpty(), "Ready condition should exist")

			By("verifying RoutingReady condition exists")
			cmd = exec.Command("kubectl", "get", "nebariapp", appName, "-n", testNamespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='RoutingReady')]}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).NotTo(BeEmpty(), "RoutingReady condition should exist")

			By("verifying all conditions have required fields")
			cmd = exec.Command("kubectl", "get", "nebariapp", appName, "-n", testNamespace, "-o", "json")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			var app map[string]interface{}
			err = json.Unmarshal([]byte(output), &app)
			Expect(err).NotTo(HaveOccurred())

			status := app["status"].(map[string]interface{})
			conditions := status["conditions"].([]interface{})

			for _, c := range conditions {
				cond := c.(map[string]interface{})
				Expect(cond["type"]).NotTo(BeEmpty(), "Condition type should be set")
				Expect(cond["status"]).NotTo(BeEmpty(), "Condition status should be set")
				Expect(cond["reason"]).NotTo(BeEmpty(), "Condition reason should be set")
				Expect(cond["message"]).NotTo(BeEmpty(), "Condition message should be set")
				Expect(cond["lastTransitionTime"]).NotTo(BeEmpty(), "lastTransitionTime should be set")
			}

			By("cleaning up")
			cmd = exec.Command("kubectl", "delete", "nebariapp", appName, "-n", testNamespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})
	})
})
