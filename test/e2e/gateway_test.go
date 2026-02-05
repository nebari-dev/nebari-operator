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
	"os/exec"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/nebari-dev/nebari-operator/test/utils"
)

var _ = Describe("Gateway and cert-manager Integration", Ordered, func() {
	BeforeAll(func() {
		// Check if Gateway API CRDs are installed
		cmd := exec.Command("kubectl", "get", "crd", "gateways.gateway.networking.k8s.io")
		_, err := utils.Run(cmd)
		if err != nil {
			Skip("Gateway API CRDs not installed - skipping Gateway integration tests")
		}
	})

	It("should have Gateway configured", func() {
		By("checking Gateway exists")
		cmd := exec.Command("kubectl", "get", "gateway", "nebari-gateway", "-n", "envoy-gateway-system",
			"-o", "jsonpath={.metadata.name}")
		output, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Gateway nebari-gateway should exist")
		Expect(output).To(Equal("nebari-gateway"))

		By("verifying Gateway has HTTP and HTTPS listeners")
		cmd = exec.Command("kubectl", "get", "gateway", "nebari-gateway", "-n", "envoy-gateway-system",
			"-o", "jsonpath={.spec.listeners[*].name}")
		output, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).To(ContainSubstring("http"))
		Expect(output).To(ContainSubstring("https"))

		By("verifying Gateway uses envoy-gateway GatewayClass")
		cmd = exec.Command("kubectl", "get", "gateway", "nebari-gateway", "-n", "envoy-gateway-system",
			"-o", "jsonpath={.spec.gatewayClassName}")
		output, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).To(Equal("envoy-gateway"))

		By("verifying HTTPS listener references TLS certificate")
		cmd = exec.Command("kubectl", "get", "gateway", "nebari-gateway", "-n", "envoy-gateway-system",
			"-o", "json")
		output, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		var gateway map[string]interface{}
		err = json.Unmarshal([]byte(output), &gateway)
		Expect(err).NotTo(HaveOccurred())

		spec := gateway["spec"].(map[string]interface{})
		listeners := spec["listeners"].([]interface{})

		var httpsListener map[string]interface{}
		for _, l := range listeners {
			listener := l.(map[string]interface{})
			if listener["name"].(string) == "https" {
				httpsListener = listener
				break
			}
		}

		Expect(httpsListener).NotTo(BeNil(), "HTTPS listener should exist")
		tls := httpsListener["tls"].(map[string]interface{})
		certRefs := tls["certificateRefs"].([]interface{})
		Expect(certRefs).To(HaveLen(1))
		certRef := certRefs[0].(map[string]interface{})
		Expect(certRef["name"]).To(Equal("nebari-gateway-tls"))
	})

	It("should have GatewayClass configured", func() {
		By("checking GatewayClass exists")
		cmd := exec.Command("kubectl", "get", "gatewayclass", "envoy-gateway",
			"-o", "jsonpath={.metadata.name}")
		output, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "GatewayClass envoy-gateway should exist")
		Expect(output).To(Equal("envoy-gateway"))

		By("verifying GatewayClass controller")
		cmd = exec.Command("kubectl", "get", "gatewayclass", "envoy-gateway",
			"-o", "jsonpath={.spec.controllerName}")
		output, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).To(Equal("gateway.envoyproxy.io/gatewayclass-controller"))
	})

	It("should have wildcard TLS certificate", func() {
		By("checking TLS secret exists")
		cmd := exec.Command("kubectl", "get", "secret", "nebari-gateway-tls", "-n", "envoy-gateway-system",
			"-o", "jsonpath={.metadata.name}")
		output, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "TLS secret nebari-gateway-tls should exist")
		Expect(output).To(Equal("nebari-gateway-tls"))

		By("verifying secret is of type TLS")
		cmd = exec.Command("kubectl", "get", "secret", "nebari-gateway-tls", "-n", "envoy-gateway-system",
			"-o", "jsonpath={.type}")
		output, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).To(Equal("kubernetes.io/tls"))

		By("verifying secret contains certificate data")
		cmd = exec.Command("kubectl", "get", "secret", "nebari-gateway-tls", "-n", "envoy-gateway-system",
			"-o", "jsonpath={.data['tls\\.crt']}")
		output, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).NotTo(BeEmpty(), "TLS certificate data should not be empty")
	})

	It("should have cert-manager Certificate resource", func() {
		By("checking if Certificate resource exists")
		cmd := exec.Command("kubectl", "get", "certificate", "nebari-gateway-cert", "-n", "envoy-gateway-system",
			"-o", "jsonpath={.metadata.name}")
		output, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Certificate nebari-gateway-cert should exist")
		Expect(output).To(Equal("nebari-gateway-cert"))

		By("verifying Certificate references the TLS secret")
		cmd = exec.Command("kubectl", "get", "certificate", "nebari-gateway-cert", "-n", "envoy-gateway-system",
			"-o", "jsonpath={.spec.secretName}")
		output, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).To(Equal("nebari-gateway-tls"))

		By("verifying Certificate status is Ready")
		cmd = exec.Command("kubectl", "get", "certificate", "nebari-gateway-cert", "-n", "envoy-gateway-system",
			"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
		output, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		Expect(output).To(Equal("True"), "Certificate should be Ready")
	})
})
