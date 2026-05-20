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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/nebari-dev/nebari-operator/test/utils"
)

var _ = Describe("NebariApp routing.tls.secretName (user-provided TLS)", Ordered, func() {
	const (
		testNamespace  = "e2e-test-tls-byo"
		appName        = "byo-tls-app"
		userSecretName = "byo-tls"
		gatewayNS      = "envoy-gateway-system"
	)
	var hostname string
	var certDir string

	BeforeAll(func() {
		hostname = fmt.Sprintf("byo-tls-%d.nebari.local", time.Now().Unix())

		By("checking if Gateway exists")
		_, err := utils.Run(exec.Command("kubectl", "get", "gateway", "nebari-gateway", "-n", gatewayNS))
		if err != nil {
			Skip("Gateway 'nebari-gateway' not found - run 'make setup' in dev/ first")
		}

		SetupTestNamespace(testNamespace)
		DeployTestApp(testNamespace)

		By("generating a self-signed TLS cert for the test hostname")
		certDir, err = os.MkdirTemp("", "byo-tls-")
		Expect(err).NotTo(HaveOccurred())
		certPath := filepath.Join(certDir, "tls.crt")
		keyPath := filepath.Join(certDir, "tls.key")
		_, err = utils.Run(exec.Command("openssl", "req", "-x509", "-nodes",
			"-newkey", "rsa:2048", "-days", "1",
			"-subj", fmt.Sprintf("/CN=%s", hostname),
			"-addext", fmt.Sprintf("subjectAltName=DNS:%s", hostname),
			"-keyout", keyPath, "-out", certPath))
		Expect(err).NotTo(HaveOccurred())

		By("creating the user-provided TLS secret in envoy-gateway-system")
		_, _ = utils.Run(exec.Command("kubectl", "delete", "secret", userSecretName,
			"-n", gatewayNS, "--ignore-not-found=true"))
		_, err = utils.Run(exec.Command("kubectl", "create", "secret", "tls", userSecretName,
			"-n", gatewayNS,
			fmt.Sprintf("--cert=%s", certPath),
			fmt.Sprintf("--key=%s", keyPath)))
		Expect(err).NotTo(HaveOccurred())
	})

	AfterAll(func() {
		_, _ = utils.Run(exec.Command("kubectl", "delete", "nebariapp", appName,
			"-n", testNamespace, "--ignore-not-found=true", "--timeout=60s"))
		_, _ = utils.Run(exec.Command("kubectl", "delete", "secret", userSecretName,
			"-n", gatewayNS, "--ignore-not-found=true"))
		CleanupTestNamespace(testNamespace)
		if certDir != "" {
			_ = os.RemoveAll(certDir)
		}
	})

	It("attaches the user-provided secret to the per-app Gateway listener and skips Certificate creation", func() {
		By("applying a NebariApp with routing.tls.secretName")
		manifest := fmt.Sprintf(`apiVersion: reconcilers.nebari.dev/v1
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
      secretName: %s
`, appName, testNamespace, hostname, userSecretName)

		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(manifest)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		By("waiting for TLSReady=True with reason UserProvidedSecretReady")
		Eventually(func(g Gomega) {
			out, err := utils.Run(exec.Command("kubectl", "get", "nebariapp", appName,
				"-n", testNamespace,
				"-o", `jsonpath={.status.conditions[?(@.type=="TLSReady")].reason}`))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(strings.TrimSpace(out)).To(Equal("UserProvidedSecretReady"))
		}, 2*time.Minute, 5*time.Second).Should(Succeed())

		By("verifying TLSReady status is True")
		out, err := utils.Run(exec.Command("kubectl", "get", "nebariapp", appName,
			"-n", testNamespace,
			"-o", `jsonpath={.status.conditions[?(@.type=="TLSReady")].status}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(out)).To(Equal("True"))

		By("verifying no cert-manager Certificate was created for this NebariApp")
		certName := fmt.Sprintf("%s-%s-cert", appName, testNamespace)
		out, _ = utils.Run(exec.Command("kubectl", "get", "certificate", certName,
			"-n", gatewayNS, "--ignore-not-found=true", "-o", "name"))
		Expect(strings.TrimSpace(out)).To(BeEmpty(),
			"expected no cert-manager Certificate when routing.tls.secretName is set")

		By("verifying the per-app Gateway listener references the user's secret")
		listenerName := fmt.Sprintf("tls-%s-%s", appName, testNamespace)
		Eventually(func(g Gomega) {
			jsonpath := fmt.Sprintf(
				`jsonpath={.spec.listeners[?(@.name=="%s")].tls.certificateRefs[0].name}`,
				listenerName)
			out, err := utils.Run(exec.Command("kubectl", "get", "gateway", "nebari-gateway",
				"-n", gatewayNS, "-o", jsonpath))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(strings.TrimSpace(out)).To(Equal(userSecretName))
		}, 1*time.Minute, 5*time.Second).Should(Succeed())
	})
})
