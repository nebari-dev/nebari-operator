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

var _ = Describe("NebariApp Reconciliation", Ordered, func() {
	var testNamespace string

	BeforeAll(func() {
		var cmd *exec.Cmd
		var err error
		var output string

		// Check if Gateway API CRDs are installed
		cmd = exec.Command("kubectl", "get", "crd", "gateways.gateway.networking.k8s.io")
		_, err = utils.Run(cmd)
		if err != nil {
			Skip("Gateway API CRDs not installed - skipping NebariApp tests")
		}

		By("installing NebariApp CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		testNamespace = "e2e-test-app"

		By("cleaning up any existing test resources")
		cmd = exec.Command("kubectl", "get", "nebariapp", "-n", testNamespace, "-o", "name")
		output, _ = utils.Run(cmd)
		if output != "" {
			cmd = exec.Command("kubectl", "delete", "nebariapp", "--all", "-n", testNamespace, "--timeout=60s")
			_, _ = utils.Run(cmd)
		}
		cmd = exec.Command("kubectl", "delete", "namespace", testNamespace, "--ignore-not-found", "--timeout=60s")
		_, _ = utils.Run(cmd)

		By("waiting for namespace to be fully deleted")
		Eventually(func() error {
			cmd = exec.Command("kubectl", "get", "namespace", testNamespace)
			_, err := utils.Run(cmd)
			return err // Will return error when namespace doesn't exist
		}, 2*time.Minute, time.Second).Should(HaveOccurred())

		By("verifying Gateway exists")
		cmd = exec.Command("kubectl", "get", "gateway", "nebari-gateway", "-n", "envoy-gateway-system")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Gateway nebari-gateway must exist")

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
		}, 2*time.Minute, time.Second).Should(Succeed())

		By("creating a test application deployment")
		var appYAML string
		appYAML, err = utils.LoadTestDataFile("test-app.yaml", map[string]string{
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
		cmd := exec.Command("kubectl", "delete", "namespace", testNamespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)

		By("undeploying the controller-manager")
		cmd = exec.Command("make", "undeploy")
		_, _ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("make", "uninstall")
		_, _ = utils.Run(cmd)
	})

	It("should reconcile a NebariApp resource successfully", func() {
		By("creating a NebariApp resource")
		nebariAppYAML, err := utils.LoadTestDataFile("nebariapp.yaml", map[string]string{
			"NAMESPACE_PLACEHOLDER": testNamespace,
			"NAME_PLACEHOLDER":      "test-nebariapp",
			"HOSTNAME_PLACEHOLDER":  "test-app.example.com",
		})
		Expect(err).NotTo(HaveOccurred(), "Failed to load nebariapp.yaml")

		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(nebariAppYAML)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create NebariApp resource")

		By("verifying that the NebariApp resource exists")
		Eventually(func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "nebariapp", "test-nebariapp", "-n", testNamespace)
			_, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
		}, time.Minute, time.Second).Should(Succeed())

		By("verifying that the NebariApp resource is reconciled")
		Eventually(func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "nebariapp", "test-nebariapp",
				"-n", testNamespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(Equal("True"), "NebariApp not ready")
		}, 3*time.Minute, 5*time.Second).Should(Succeed())

		By("verifying that HTTPRoute was created")
		Eventually(func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "httproute", "test-nebariapp-route",
				"-n", testNamespace, "-o", "jsonpath={.spec.hostnames[0]}")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(Equal("test-app.example.com"))
		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		By("verifying HTTPRoute references the correct gateway")
		cmd = exec.Command("kubectl", "get", "httproute", "test-nebariapp-route",
			"-n", testNamespace, "-o", "jsonpath={.spec.parentRefs[0].name}")
		output, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).To(Equal("nebari-gateway"))

		By("verifying HTTPRoute references gateway in correct namespace")
		cmd = exec.Command("kubectl", "get", "httproute", "test-nebariapp-route",
			"-n", testNamespace, "-o", "jsonpath={.spec.parentRefs[0].namespace}")
		output, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).To(Equal("envoy-gateway-system"))

		By("verifying HTTPRoute backend references correct service")
		cmd = exec.Command("kubectl", "get", "httproute", "test-nebariapp-route",
			"-n", testNamespace, "-o", "jsonpath={.spec.rules[0].backendRefs[0].name}")
		output, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).To(Equal("test-app"))

		By("verifying HTTPRoute uses HTTPS listener by default (sectionName=https)")
		cmd = exec.Command("kubectl", "get", "httproute", "test-nebariapp-route",
			"-n", testNamespace, "-o", "jsonpath={.spec.parentRefs[0]}")
		output, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).To(ContainSubstring("https"))

		By("verifying RoutingReady condition is True")
		cmd = exec.Command("kubectl", "get", "nebariapp", "test-nebariapp",
			"-n", testNamespace, "-o", "jsonpath={.status.conditions[?(@.type=='RoutingReady')].status}")
		output, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).To(Equal("True"))
	})

	It("should create HTTPRoute with HTTP listener when TLS is disabled", func() {
		By("creating a NebariApp resource with TLS disabled")
		nebariAppYAML, err := utils.LoadTestDataFile("nebariapp-tls-disabled.yaml", map[string]string{
			"NAMESPACE_PLACEHOLDER": testNamespace,
			"NAME_PLACEHOLDER":      "test-tls-disabled",
			"HOSTNAME_PLACEHOLDER":  "tls-disabled.example.com",
		})
		Expect(err).NotTo(HaveOccurred(), "Failed to load nebariapp-tls-disabled.yaml")

		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(nebariAppYAML)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create NebariApp resource")

		By("verifying that the NebariApp is reconciled")
		Eventually(func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "nebariapp", "test-tls-disabled",
				"-n", testNamespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(Equal("True"))
		}, 3*time.Minute, 5*time.Second).Should(Succeed())

		By("verifying HTTPRoute was created")
		Eventually(func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "httproute", "test-tls-disabled-route", "-n", testNamespace)
			_, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		By("verifying HTTPRoute references HTTP listener (sectionName=http)")
		cmd = exec.Command("kubectl", "get", "httproute", "test-tls-disabled-route",
			"-n", testNamespace, "-o", "jsonpath={.spec.parentRefs[0]}")
		output, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).To(ContainSubstring("http"))
		Expect(output).NotTo(ContainSubstring("https"))

		By("verifying HTTPRoute annotation reflects TLS disabled")
		cmd = exec.Command("kubectl", "get", "httproute", "test-tls-disabled-route",
			"-n", testNamespace, "-o", "jsonpath={.metadata.annotations}")
		output, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).To(ContainSubstring("nebari.dev/tls-enabled"))
		Expect(output).To(ContainSubstring("false"))

		By("cleaning up test-tls-disabled resource")
		cmd = exec.Command("kubectl", "delete", "nebariapp", "test-tls-disabled", "-n", testNamespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)
	})

	It("should handle NebariApp deletion and cleanup HTTPRoute", func() {
		By("deleting the NebariApp resource")
		cmd := exec.Command("kubectl", "delete", "nebariapp", "test-nebariapp", "-n", testNamespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to delete NebariApp")

		By("verifying HTTPRoute is deleted")
		Eventually(func() error {
			cmd := exec.Command("kubectl", "get", "httproute", "test-nebariapp-route", "-n", testNamespace)
			_, err := utils.Run(cmd)
			return err // Will return error when HTTPRoute doesn't exist
		}, 2*time.Minute, 5*time.Second).Should(HaveOccurred())
	})
})
