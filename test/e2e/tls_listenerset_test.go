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

// This suite exercises the per-app ListenerSet cutover (ADR-0011 Option 2) end to
// end against a real Envoy Gateway. It needs the standard ListenerSet CRD (Envoy
// Gateway v1.8.2+) and drives the cert-manager TLS path, so it is Serial and
// self-contained: it turns on the operator's ClusterIssuer and the Gateway's
// spec.allowedListeners in BeforeAll and reverts both in AfterAll, so the rest of
// the suite keeps seeing the default (no per-app cert-manager TLS) behaviour.
var _ = Describe("NebariApp per-app ListenerSet cutover (cert-manager TLS)", Ordered, Serial, func() {
	const (
		testNamespace  = "e2e-test-listenerset"
		appName        = "cutover-app"
		userSecretName = "cutover-byo-tls"
		gatewayNS      = "envoy-gateway-system"
		operatorNS     = "nebari-operator-system"
		operatorDeploy = "nebari-operator-controller-manager"
		clusterIssuer  = "selfsigned-issuer"
	)
	var (
		hostname     string
		listenerName string // legacy shared-Gateway listener name: tls-<app>-<ns>
		lsSelector   string // label selector for the per-app ListenerSet
		certDir      string
	)

	// listenerSetProgrammed returns the set-level Programmed condition status of the
	// app's ListenerSet, or "" if the ListenerSet does not exist.
	listenerSetProgrammed := func() string {
		out, _ := utils.Run(exec.Command("kubectl", "get", "listenerset",
			"-n", testNamespace, "-l", lsSelector,
			"-o", `jsonpath={.items[0].status.conditions[?(@.type=="Programmed")].status}`))
		return strings.TrimSpace(out)
	}

	// listenerSetNames returns the app's ListenerSet resource names ("" when none).
	listenerSetNames := func() string {
		out, _ := utils.Run(exec.Command("kubectl", "get", "listenerset",
			"-n", testNamespace, "-l", lsSelector, "-o", "name"))
		return strings.TrimSpace(out)
	}

	// gatewayHasLegacyListener reports whether the shared Gateway still carries this
	// app's legacy per-app HTTPS listener.
	gatewayHasLegacyListener := func() bool {
		out, _ := utils.Run(exec.Command("kubectl", "get", "gateway", "nebari-gateway",
			"-n", gatewayNS,
			"-o", fmt.Sprintf(`jsonpath={.spec.listeners[?(@.name=="%s")].name}`, listenerName)))
		return strings.TrimSpace(out) != ""
	}

	BeforeAll(func() {
		hostname = fmt.Sprintf("cutover-%d.nebari.local", time.Now().Unix())
		listenerName = fmt.Sprintf("tls-%s-%s", appName, testNamespace)
		lsSelector = fmt.Sprintf("nebari.dev/nebariapp-name=%s", appName)

		By("checking the standard ListenerSet CRD is present (Envoy Gateway v1.8.2+)")
		if _, err := utils.Run(exec.Command("kubectl", "get", "crd",
			"listenersets.gateway.networking.k8s.io")); err != nil {
			Skip("standard ListenerSet CRD not present - needs Envoy Gateway v1.8.2+")
		}

		By("checking the shared Gateway exists")
		if _, err := utils.Run(exec.Command("kubectl", "get", "gateway", "nebari-gateway",
			"-n", gatewayNS)); err != nil {
			Skip("Gateway 'nebari-gateway' not found - run 'make setup' in dev/ first")
		}

		By("checking the ClusterIssuer exists")
		if _, err := utils.Run(exec.Command("kubectl", "get", "clusterissuer", clusterIssuer)); err != nil {
			Skip(fmt.Sprintf("ClusterIssuer %q not found", clusterIssuer))
		}

		SetupTestNamespace(testNamespace)
		DeployTestApp(testNamespace)

		By("generating a self-signed TLS cert for the user-secret transition")
		var err error
		certDir, err = os.MkdirTemp("", "cutover-byo-")
		Expect(err).NotTo(HaveOccurred())
		certPath := filepath.Join(certDir, "tls.crt")
		keyPath := filepath.Join(certDir, "tls.key")
		_, err = utils.Run(exec.Command("openssl", "req", "-x509", "-nodes",
			"-newkey", "rsa:2048", "-days", "1",
			"-subj", fmt.Sprintf("/CN=%s", hostname),
			"-addext", fmt.Sprintf("subjectAltName=DNS:%s", hostname),
			"-keyout", keyPath, "-out", certPath))
		Expect(err).NotTo(HaveOccurred())
		_, _ = utils.Run(exec.Command("kubectl", "delete", "secret", userSecretName,
			"-n", gatewayNS, "--ignore-not-found=true"))
		_, err = utils.Run(exec.Command("kubectl", "create", "secret", "tls", userSecretName,
			"-n", gatewayNS,
			fmt.Sprintf("--cert=%s", certPath), fmt.Sprintf("--key=%s", keyPath)))
		Expect(err).NotTo(HaveOccurred())

		By("allowing per-app ListenerSets from the test namespace on the shared Gateway")
		_, err = utils.Run(exec.Command("kubectl", "patch", "gateway", "nebari-gateway",
			"-n", gatewayNS, "--type", "merge", "-p", fmt.Sprintf(
				`{"spec":{"allowedListeners":{"namespaces":{"from":"Selector",`+
					`"selector":{"matchLabels":{"kubernetes.io/metadata.name":%q}}}}}}`, testNamespace)))
		Expect(err).NotTo(HaveOccurred())

		By("enabling the operator's ClusterIssuer so the cert-manager path (and ListenerSet) is used")
		_, err = utils.Run(exec.Command("kubectl", "set", "env",
			fmt.Sprintf("deployment/%s", operatorDeploy), "-n", operatorNS,
			fmt.Sprintf("TLS_CLUSTER_ISSUER_NAME=%s", clusterIssuer)))
		Expect(err).NotTo(HaveOccurred())
		_, err = utils.Run(exec.Command("kubectl", "rollout", "status",
			fmt.Sprintf("deployment/%s", operatorDeploy), "-n", operatorNS, "--timeout=120s"))
		Expect(err).NotTo(HaveOccurred())
	})

	AfterAll(func() {
		_, _ = utils.Run(exec.Command("kubectl", "delete", "nebariapp", appName,
			"-n", testNamespace, "--ignore-not-found=true", "--timeout=60s"))

		By("reverting the operator ClusterIssuer override")
		_, _ = utils.Run(exec.Command("kubectl", "set", "env",
			fmt.Sprintf("deployment/%s", operatorDeploy), "-n", operatorNS,
			"TLS_CLUSTER_ISSUER_NAME-"))
		_, _ = utils.Run(exec.Command("kubectl", "rollout", "status",
			fmt.Sprintf("deployment/%s", operatorDeploy), "-n", operatorNS, "--timeout=120s"))

		By("reverting the Gateway allowedListeners override")
		_, _ = utils.Run(exec.Command("kubectl", "patch", "gateway", "nebari-gateway",
			"-n", gatewayNS, "--type", "json", "-p",
			`[{"op":"remove","path":"/spec/allowedListeners"}]`))

		_, _ = utils.Run(exec.Command("kubectl", "delete", "secret", userSecretName,
			"-n", gatewayNS, "--ignore-not-found=true"))
		CleanupTestNamespace(testNamespace)
		if certDir != "" {
			_ = os.RemoveAll(certDir)
		}
	})

	It("cuts a cert-manager TLS app over to a per-app ListenerSet and retires the legacy listener", func() {
		By("applying a NebariApp with cert-manager TLS")
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
`, appName, testNamespace, hostname)
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(manifest)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())

		By("waiting for the per-app ListenerSet to be Programmed")
		Eventually(listenerSetProgrammed, 3*time.Minute, 5*time.Second).Should(Equal("True"))

		By("verifying the HTTPRoute reparented to the ListenerSet")
		Eventually(func(g Gomega) {
			out, err := utils.Run(exec.Command("kubectl", "get", "httproute",
				fmt.Sprintf("%s-route", appName), "-n", testNamespace,
				"-o", "jsonpath={.spec.parentRefs[0].kind}"))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(strings.TrimSpace(out)).To(Equal("ListenerSet"))
		}, 1*time.Minute, 5*time.Second).Should(Succeed())

		By("verifying the legacy shared-Gateway listener was removed on cutover")
		Eventually(gatewayHasLegacyListener, 1*time.Minute, 5*time.Second).Should(BeFalse())

		By("verifying TLSReady is True")
		out, err := utils.Run(exec.Command("kubectl", "get", "nebariapp", appName,
			"-n", testNamespace,
			"-o", `jsonpath={.status.conditions[?(@.type=="TLSReady")].status}`))
		Expect(err).NotTo(HaveOccurred())
		Expect(strings.TrimSpace(out)).To(Equal("True"))
	})

	It("removes the ListenerSet and falls back to the legacy listener when switching to a user-provided secret", func() {
		By("setting routing.tls.secretName on the cut-over app")
		_, err := utils.Run(exec.Command("kubectl", "patch", "nebariapp", appName,
			"-n", testNamespace, "--type", "merge", "-p",
			fmt.Sprintf(`{"spec":{"routing":{"tls":{"secretName":%q}}}}`, userSecretName)))
		Expect(err).NotTo(HaveOccurred())

		By("verifying the ListenerSet is deleted (no longer claims the hostname)")
		Eventually(listenerSetNames, 2*time.Minute, 5*time.Second).Should(BeEmpty())

		By("verifying the legacy Gateway listener is back and references the user secret")
		Eventually(func(g Gomega) {
			out, err := utils.Run(exec.Command("kubectl", "get", "gateway", "nebari-gateway",
				"-n", gatewayNS, "-o", fmt.Sprintf(
					`jsonpath={.spec.listeners[?(@.name=="%s")].tls.certificateRefs[0].name}`, listenerName)))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(strings.TrimSpace(out)).To(Equal(userSecretName))
		}, 1*time.Minute, 5*time.Second).Should(Succeed())
	})

	It("removes both the ListenerSet and the legacy listener when TLS is disabled", func() {
		By("returning the app to the cert-manager path and waiting for re-cutover")
		_, err := utils.Run(exec.Command("kubectl", "patch", "nebariapp", appName,
			"-n", testNamespace, "--type", "json", "-p",
			`[{"op":"remove","path":"/spec/routing/tls/secretName"}]`))
		Expect(err).NotTo(HaveOccurred())
		Eventually(listenerSetProgrammed, 3*time.Minute, 5*time.Second).Should(Equal("True"))

		By("disabling TLS on the cut-over app")
		_, err = utils.Run(exec.Command("kubectl", "patch", "nebariapp", appName,
			"-n", testNamespace, "--type", "merge", "-p",
			`{"spec":{"routing":{"tls":{"enabled":false}}}}`))
		Expect(err).NotTo(HaveOccurred())

		By("verifying the ListenerSet is deleted")
		Eventually(listenerSetNames, 2*time.Minute, 5*time.Second).Should(BeEmpty())

		By("verifying no legacy Gateway listener remains for the app")
		Eventually(gatewayHasLegacyListener, 1*time.Minute, 5*time.Second).Should(BeFalse())

		By("verifying the HTTPRoute now targets the plain HTTP listener")
		Eventually(func(g Gomega) {
			out, err := utils.Run(exec.Command("kubectl", "get", "httproute",
				fmt.Sprintf("%s-route", appName), "-n", testNamespace,
				"-o", "jsonpath={.spec.parentRefs[0].sectionName}"))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(strings.TrimSpace(out)).To(Equal("http"))
		}, 1*time.Minute, 5*time.Second).Should(Succeed())
	})
})
