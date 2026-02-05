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

var _ = Describe("NebariApp Authentication", Ordered, func() {
	const testNamespace = "e2e-test-auth"
	const keycloakNamespace = "keycloak"

	BeforeAll(func() {
		var cmd *exec.Cmd
		var err error

		By("installing NebariApp CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("cleaning up any existing auth test resources")
		cmd = exec.Command("kubectl", "delete", "namespace", testNamespace, "--ignore-not-found", "--timeout=60s")
		_, _ = utils.Run(cmd)

		By("waiting for namespace to be fully deleted")
		Eventually(func() error {
			cmd = exec.Command("kubectl", "get", "namespace", testNamespace)
			_, err := utils.Run(cmd)
			return err
		}, 2*time.Minute, time.Second).Should(HaveOccurred())

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

		By("verifying operator can load Keycloak configuration")
		// Check that the operator pod logged successful config loading
		Eventually(func(g Gomega) {
			cmd := exec.Command("kubectl", "logs", "-n", "nebari-operator-system",
				"-l", "control-plane=controller-manager",
				"--tail=100")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			// Should not have config errors
			g.Expect(output).NotTo(ContainSubstring("failed to load config"))
			g.Expect(output).NotTo(ContainSubstring("config error"))
		}, 1*time.Minute, 5*time.Second).Should(Succeed())

		By("creating test namespace")
		cmd = exec.Command("kubectl", "create", "namespace", testNamespace)
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("labeling namespace for Operator management")
		cmd = exec.Command("kubectl", "label", "namespace", testNamespace, "nebari.dev/managed=true")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace")

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
		By("cleaning up auth test resources")
		cmd := exec.Command("kubectl", "delete", "namespace", testNamespace, "--ignore-not-found")
		_, _ = utils.Run(cmd)

		By("undeploying the controller-manager")
		cmd = exec.Command("make", "undeploy")
		_, _ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("make", "uninstall")
		_, _ = utils.Run(cmd)
	})

	Context("Operator Configuration", func() {
		It("should have correct Keycloak environment variables configured", func() {
			By("checking controller-manager deployment has Keycloak env vars")
			cmd := exec.Command("kubectl", "get", "deployment", "nebari-operator-controller-manager",
				"-n", "nebari-operator-system", "-o", "json")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			// Verify critical Keycloak environment variables are set
			Expect(output).To(ContainSubstring("KEYCLOAK_ENABLED"))
			Expect(output).To(ContainSubstring("KEYCLOAK_URL"))
			Expect(output).To(ContainSubstring("KEYCLOAK_REALM"))
			Expect(output).To(ContainSubstring("KEYCLOAK_ADMIN_SECRET_NAME"))
			Expect(output).To(ContainSubstring("KEYCLOAK_ADMIN_SECRET_NAMESPACE"))
			Expect(output).To(ContainSubstring("keycloak-admin-credentials"))
			Expect(output).To(ContainSubstring("keycloak"))
		})

		It("should verify Keycloak admin secret exists", func() {
			By("checking that Keycloak admin secret is available")
			cmd := exec.Command("kubectl", "get", "secret", "keycloak-admin-credentials", "-n", keycloakNamespace)
			_, err := utils.Run(cmd)
			if err != nil {
				Skip("Keycloak admin secret not found - auth tests requiring Keycloak will be skipped")
			}

			By("verifying secret has required keys (supports both formats)")
			cmd = exec.Command("kubectl", "get", "secret", "keycloak-admin-credentials",
				"-n", keycloakNamespace, "-o", "jsonpath={.data}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			// Secret should have either username/password OR admin-username/admin-password
			hasStandardKeys := strings.Contains(output, "username") && strings.Contains(output, "password")
			hasAdminKeys := strings.Contains(output, "admin-username") && strings.Contains(output, "admin-password")
			Expect(hasStandardKeys || hasAdminKeys).To(BeTrue(), "Secret should contain either username/password or admin-username/admin-password")
		})

		It("should verify Keycloak is accessible", func() {
			By("checking Keycloak service exists")
			cmd := exec.Command("kubectl", "get", "service", "-n", keycloakNamespace)
			output, err := utils.Run(cmd)
			if err != nil {
				Skip("Keycloak namespace not found - skipping Keycloak connectivity test")
			}
			Expect(output).To(ContainSubstring("keycloak"))
		})
	})

	Context("When authentication is disabled (default)", func() {
		It("should not create SecurityPolicy", func() {
			By("creating a NebariApp without auth configuration")
			nebariAppYAML, err := utils.LoadTestDataFile("nebariapp-no-auth.yaml", map[string]string{
				"NAMESPACE_PLACEHOLDER": testNamespace,
				"NAME_PLACEHOLDER":      "test-no-auth",
				"HOSTNAME_PLACEHOLDER":  "no-auth.example.com",
			})
			Expect(err).NotTo(HaveOccurred(), "Failed to load nebariapp-no-auth.yaml")

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nebariAppYAML)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create NebariApp resource")

			By("verifying that the NebariApp is reconciled")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "nebariapp", "test-no-auth",
					"-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying AuthReady condition is False with reason AuthDisabled")
			cmd = exec.Command("kubectl", "get", "nebariapp", "test-no-auth",
				"-n", testNamespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='AuthReady')].reason}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(Equal("AuthDisabled"))

			By("verifying SecurityPolicy was not created")
			cmd = exec.Command("kubectl", "get", "securitypolicy", "test-no-auth-security", "-n", testNamespace)
			_, err = utils.Run(cmd)
			Expect(err).To(HaveOccurred(), "SecurityPolicy should not exist when auth is disabled")

			By("cleaning up test-no-auth resource")
			cmd = exec.Command("kubectl", "delete", "nebariapp", "test-no-auth", "-n", testNamespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})
	})

	Context("When authentication is enabled with Keycloak", func() {
		var keycloakAvailable bool

		BeforeAll(func() {
			By("checking if Keycloak is available")
			cmd := exec.Command("kubectl", "get", "namespace", keycloakNamespace)
			_, err := utils.Run(cmd)
			keycloakAvailable = (err == nil)

			if !keycloakAvailable {
				Skip("Keycloak namespace not found - skipping Keycloak auth tests")
			}

			By("checking if Keycloak admin secret exists")
			cmd = exec.Command("kubectl", "get", "secret", "keycloak-admin-credentials", "-n", keycloakNamespace)
			_, err = utils.Run(cmd)
			if err != nil {
				Skip("Keycloak admin secret not found - skipping Keycloak auth tests")
			}
		})

		It("should provision OIDC client and create SecurityPolicy", func() {
			By("creating a NebariApp with Keycloak auth enabled")
			nebariAppYAML := `
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: test-keycloak-auth
  namespace: ` + testNamespace + `
spec:
  hostname: keycloak-auth.example.com
  service:
    name: test-app
    port: 80
  routing:
    tls:
      enabled: true
  auth:
    enabled: true
    provider: keycloak
    provisionClient: true
    scopes:
      - openid
      - profile
      - email
      - groups
`
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nebariAppYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create NebariApp resource")

			By("verifying that the NebariApp is reconciled")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "nebariapp", "test-keycloak-auth",
					"-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}, 5*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying AuthReady condition is True")
			cmd = exec.Command("kubectl", "get", "nebariapp", "test-keycloak-auth",
				"-n", testNamespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='AuthReady')].status}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(Equal("True"))

			By("verifying OIDC client secret was created")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "secret", "test-keycloak-auth-oidc-client", "-n", testNamespace)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
			}, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying client secret contains client-secret key")
			cmd = exec.Command("kubectl", "get", "secret", "test-keycloak-auth-oidc-client",
				"-n", testNamespace, "-o", "jsonpath={.data.client-secret}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).NotTo(BeEmpty(), "client-secret should not be empty")

			By("verifying SecurityPolicy was created")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "securitypolicy", "test-keycloak-auth-security", "-n", testNamespace)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
			}, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying SecurityPolicy targets the HTTPRoute")
			cmd = exec.Command("kubectl", "get", "securitypolicy", "test-keycloak-auth-security",
				"-n", testNamespace, "-o", "jsonpath={.spec.targetRefs[0].name}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(Equal("test-keycloak-auth-route"))

			By("verifying SecurityPolicy has OIDC configuration")
			cmd = exec.Command("kubectl", "get", "securitypolicy", "test-keycloak-auth-security",
				"-n", testNamespace, "-o", "jsonpath={.spec.oidc.provider.issuer}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(ContainSubstring("keycloak"))

			By("verifying SecurityPolicy references client secret")
			cmd = exec.Command("kubectl", "get", "securitypolicy", "test-keycloak-auth-security",
				"-n", testNamespace, "-o", "jsonpath={.spec.oidc.clientSecret.name}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(Equal("test-keycloak-auth-oidc-client"))

			By("verifying SecurityPolicy has correct scopes")
			cmd = exec.Command("kubectl", "get", "securitypolicy", "test-keycloak-auth-security",
				"-n", testNamespace, "-o", "jsonpath={.spec.oidc.scopes}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(ContainSubstring("openid"))
			Expect(output).To(ContainSubstring("profile"))
			Expect(output).To(ContainSubstring("email"))
			Expect(output).To(ContainSubstring("groups"))

			By("cleaning up test-keycloak-auth resource")
			cmd = exec.Command("kubectl", "delete", "nebariapp", "test-keycloak-auth", "-n", testNamespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)

			By("verifying SecurityPolicy is cleaned up")
			Eventually(func() error {
				cmd := exec.Command("kubectl", "get", "securitypolicy", "test-keycloak-auth-security", "-n", testNamespace)
				_, err := utils.Run(cmd)
				return err
			}, 2*time.Minute, 5*time.Second).Should(HaveOccurred())
		})

		It("should fail validation if client secret does not exist", func() {
			By("creating a NebariApp with provisionClient=false and no secret")
			nebariAppYAML := `
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: test-no-secret
  namespace: ` + testNamespace + `
spec:
  hostname: no-secret.example.com
  service:
    name: test-app
    port: 80
  auth:
    enabled: true
    provider: keycloak
    provisionClient: false
`
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nebariAppYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create NebariApp resource")

			By("verifying AuthReady condition is False with reason ValidationFailed")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "nebariapp", "test-no-secret",
					"-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='AuthReady')].reason}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("ValidationFailed"))
			}, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying error message mentions missing secret")
			cmd = exec.Command("kubectl", "get", "nebariapp", "test-no-secret",
				"-n", testNamespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='AuthReady')].message}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(ContainSubstring("secret"))
			Expect(output).To(ContainSubstring("not found"))

			By("cleaning up test-no-secret resource")
			cmd = exec.Command("kubectl", "delete", "nebariapp", "test-no-secret", "-n", testNamespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})
	})

	Context("When using generic OIDC provider", func() {
		It("should create SecurityPolicy with manually provisioned client", func() {
			By("creating a client secret manually")
			secretYAML := `
apiVersion: v1
kind: Secret
metadata:
  name: test-generic-oidc-client
  namespace: ` + testNamespace + `
type: Opaque
stringData:
  client-secret: "mock-client-secret-for-testing"
`
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(secretYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create client secret")

			By("creating a NebariApp with generic-oidc provider")
			nebariAppYAML := `
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: test-generic-oidc
  namespace: ` + testNamespace + `
spec:
  hostname: generic-oidc.example.com
  service:
    name: test-app
    port: 80
  routing:
    tls:
      enabled: true
  auth:
    enabled: true
    provider: generic-oidc
    provisionClient: false
    issuerURL: https://accounts.google.com
    clientSecretRef: test-generic-oidc-client
    scopes:
      - openid
      - profile
      - email
`
			cmd = exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nebariAppYAML)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create NebariApp resource")

			By("verifying that the NebariApp is reconciled")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "nebariapp", "test-generic-oidc",
					"-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}, 3*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying AuthReady condition is True")
			cmd = exec.Command("kubectl", "get", "nebariapp", "test-generic-oidc",
				"-n", testNamespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='AuthReady')].status}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(Equal("True"))

			By("verifying SecurityPolicy was created")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "securitypolicy", "test-generic-oidc-security", "-n", testNamespace)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
			}, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying SecurityPolicy has correct issuer URL")
			cmd = exec.Command("kubectl", "get", "securitypolicy", "test-generic-oidc-security",
				"-n", testNamespace, "-o", "jsonpath={.spec.oidc.provider.issuer}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(Equal("https://accounts.google.com"))

			By("verifying SecurityPolicy references the manually created secret")
			cmd = exec.Command("kubectl", "get", "securitypolicy", "test-generic-oidc-security",
				"-n", testNamespace, "-o", "jsonpath={.spec.oidc.clientSecret.name}")
			output, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(Equal("test-generic-oidc-client"))

			By("cleaning up test-generic-oidc resource")
			cmd = exec.Command("kubectl", "delete", "nebariapp", "test-generic-oidc", "-n", testNamespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)

			By("cleaning up client secret")
			cmd = exec.Command("kubectl", "delete", "secret", "test-generic-oidc-client", "-n", testNamespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})

		It("should fail if issuerURL is not provided", func() {
			By("creating a NebariApp with generic-oidc but no issuerURL")
			nebariAppYAML := `
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: test-no-issuer
  namespace: ` + testNamespace + `
spec:
  hostname: no-issuer.example.com
  service:
    name: test-app
    port: 80
  auth:
    enabled: true
    provider: generic-oidc
    provisionClient: false
`
			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nebariAppYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create NebariApp resource")

			By("verifying AuthReady condition indicates error")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "nebariapp", "test-no-issuer",
					"-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='AuthReady')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("False"))
			}, 2*time.Minute, 5*time.Second).Should(Succeed())

			By("verifying error message mentions missing issuerURL")
			cmd = exec.Command("kubectl", "get", "nebariapp", "test-no-issuer",
				"-n", testNamespace,
				"-o", "jsonpath={.status.conditions[?(@.type=='AuthReady')].message}")
			output, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
			Expect(output).To(ContainSubstring("issuerURL"))

			By("cleaning up test-no-issuer resource")
			cmd = exec.Command("kubectl", "delete", "nebariapp", "test-no-issuer", "-n", testNamespace, "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})
	})
})
