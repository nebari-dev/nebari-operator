//go:build e2e
// +build e2e

/*
Copyright 2025.

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
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/nebari-dev/nic-operator/test/utils"
)

// namespace where the project is deployed in
const namespace = "nic-operator-system"

// serviceAccountName created for the project
const serviceAccountName = "nic-operator-controller-manager"

// metricsServiceName is the name of the metrics service of the project
const metricsServiceName = "nic-operator-controller-manager-metrics-service"

// metricsRoleBindingName is the name of the RBAC that will be created to allow get the metrics data
const metricsRoleBindingName = "nic-operator-metrics-binding"

var _ = Describe("Manager", Ordered, func() {
	var controllerPodName string

	// Before running the tests, set up the environment by creating the namespace,
	// enforce the restricted security policy to the namespace, installing CRDs,
	// and deploying the controller.
	BeforeAll(func() {
		By("cleaning up any existing test namespace")
		cmd := exec.Command("kubectl", "delete", "ns", namespace, "--ignore-not-found", "--timeout=60s")
		_, _ = utils.Run(cmd)

		By("creating manager namespace")
		cmd = exec.Command("kubectl", "create", "ns", namespace)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to create namespace")

		By("labeling the namespace to enforce the restricted security policy")
		cmd = exec.Command("kubectl", "label", "--overwrite", "ns", namespace,
			"pod-security.kubernetes.io/enforce=restricted")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to label namespace with restricted policy")

		By("installing CRDs")
		cmd = exec.Command("make", "install")
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to install CRDs")

		By("deploying the controller-manager")
		cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", projectImage))
		_, err = utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), "Failed to deploy the controller-manager")
	})

	// After all tests have been executed, clean up by undeploying the controller, uninstalling CRDs,
	// and deleting the namespace.
	AfterAll(func() {
		By("cleaning up the curl pod for metrics")
		cmd := exec.Command("kubectl", "delete", "pod", "curl-metrics", "-n", namespace)
		_, _ = utils.Run(cmd)

		By("undeploying the controller-manager")
		cmd = exec.Command("make", "undeploy")
		_, _ = utils.Run(cmd)

		By("uninstalling CRDs")
		cmd = exec.Command("make", "uninstall")
		_, _ = utils.Run(cmd)

		By("removing manager namespace")
		cmd = exec.Command("kubectl", "delete", "ns", namespace)
		_, _ = utils.Run(cmd)
	})

	// After each test, check for failures and collect logs, events,
	// and pod descriptions for debugging.
	AfterEach(func() {
		specReport := CurrentSpecReport()
		if specReport.Failed() {
			By("Fetching controller manager pod logs")
			cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
			controllerLogs, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Controller logs:\n %s", controllerLogs)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Controller logs: %s", err)
			}

			By("Fetching Kubernetes events")
			cmd = exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
			eventsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Kubernetes events:\n%s", eventsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get Kubernetes events: %s", err)
			}

			By("Fetching curl-metrics logs")
			cmd = exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
			metricsOutput, err := utils.Run(cmd)
			if err == nil {
				_, _ = fmt.Fprintf(GinkgoWriter, "Metrics logs:\n %s", metricsOutput)
			} else {
				_, _ = fmt.Fprintf(GinkgoWriter, "Failed to get curl-metrics logs: %s", err)
			}

			By("Fetching controller manager pod description")
			cmd = exec.Command("kubectl", "describe", "pod", controllerPodName, "-n", namespace)
			podDescription, err := utils.Run(cmd)
			if err == nil {
				fmt.Println("Pod description:\n", podDescription)
			} else {
				fmt.Println("Failed to describe controller pod")
			}
		}
	})

	SetDefaultEventuallyTimeout(2 * time.Minute)
	SetDefaultEventuallyPollingInterval(time.Second)

	Context("Manager", func() {
		It("should run successfully", func() {
			By("validating that the controller-manager pod is running as expected")
			verifyControllerUp := func(g Gomega) {
				// Get the name of the controller-manager pod
				cmd := exec.Command("kubectl", "get",
					"pods", "-l", "control-plane=controller-manager",
					"-o", "go-template={{ range .items }}"+
						"{{ if not .metadata.deletionTimestamp }}"+
						"{{ .metadata.name }}"+
						"{{ \"\\n\" }}{{ end }}{{ end }}",
					"-n", namespace,
				)

				podOutput, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve controller-manager pod information")
				podNames := utils.GetNonEmptyLines(podOutput)
				g.Expect(podNames).To(HaveLen(1), "expected 1 controller pod running")
				controllerPodName = podNames[0]
				g.Expect(controllerPodName).To(ContainSubstring("controller-manager"))

				// Validate the pod's status
				cmd = exec.Command("kubectl", "get",
					"pods", controllerPodName, "-o", "jsonpath={.status.phase}",
					"-n", namespace,
				)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Running"), "Incorrect controller-manager pod status")
			}
			Eventually(verifyControllerUp).Should(Succeed())
		})

		It("should ensure the metrics endpoint is serving metrics", func() {
			By("creating a ClusterRoleBinding for the service account to allow access to metrics")
			cmd := exec.Command("kubectl", "create", "clusterrolebinding", metricsRoleBindingName,
				"--clusterrole=nic-operator-metrics-reader",
				fmt.Sprintf("--serviceaccount=%s:%s", namespace, serviceAccountName),
			)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create ClusterRoleBinding")

			By("validating that the metrics service is available")
			cmd = exec.Command("kubectl", "get", "service", metricsServiceName, "-n", namespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Metrics service should exist")

			By("getting the service account token")
			token, err := serviceAccountToken()
			Expect(err).NotTo(HaveOccurred())
			Expect(token).NotTo(BeEmpty())

			By("ensuring the controller pod is ready")
			verifyControllerPodReady := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pod", controllerPodName, "-n", namespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"), "Controller pod not ready")
			}
			Eventually(verifyControllerPodReady, 3*time.Minute, time.Second).Should(Succeed())

			By("verifying that the controller manager is serving the metrics server")
			verifyMetricsServerStarted := func(g Gomega) {
				cmd := exec.Command("kubectl", "logs", controllerPodName, "-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("Serving metrics server"),
					"Metrics server not yet started")
			}
			Eventually(verifyMetricsServerStarted, 3*time.Minute, time.Second).Should(Succeed())

			// +kubebuilder:scaffold:e2e-metrics-webhooks-readiness

			By("creating the curl-metrics pod to access the metrics endpoint")
			cmd = exec.Command("kubectl", "run", "curl-metrics", "--restart=Never",
				"--namespace", namespace,
				"--image=curlimages/curl:latest",
				"--overrides",
				fmt.Sprintf(`{
					"spec": {
						"containers": [{
							"name": "curl",
							"image": "curlimages/curl:latest",
							"command": ["/bin/sh", "-c"],
							"args": ["curl -v -k -H 'Authorization: Bearer %s' https://%s.%s.svc.cluster.local:8443/metrics"],
							"securityContext": {
								"readOnlyRootFilesystem": true,
								"allowPrivilegeEscalation": false,
								"capabilities": {
									"drop": ["ALL"]
								},
								"runAsNonRoot": true,
								"runAsUser": 1000,
								"seccompProfile": {
									"type": "RuntimeDefault"
								}
							}
						}],
						"serviceAccountName": "%s"
					}
				}`, token, metricsServiceName, namespace, serviceAccountName))
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create curl-metrics pod")

			By("waiting for the curl-metrics pod to complete.")
			verifyCurlUp := func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "pods", "curl-metrics",
					"-o", "jsonpath={.status.phase}",
					"-n", namespace)
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("Succeeded"), "curl pod in wrong status")
			}
			Eventually(verifyCurlUp, 5*time.Minute).Should(Succeed())

			By("getting the metrics by checking curl-metrics logs")
			verifyMetricsAvailable := func(g Gomega) {
				metricsOutput, err := getMetricsOutput()
				g.Expect(err).NotTo(HaveOccurred(), "Failed to retrieve logs from curl pod")
				g.Expect(metricsOutput).NotTo(BeEmpty())
				g.Expect(metricsOutput).To(ContainSubstring("< HTTP/1.1 200 OK"))
			}
			Eventually(verifyMetricsAvailable, 2*time.Minute).Should(Succeed())
		})

		// +kubebuilder:scaffold:e2e-webhooks-checks
	})

	Context("NicApp Reconciliation", func() {
		var testNamespace string

		BeforeAll(func() {
			testNamespace = "e2e-test-app"

			By("cleaning up any existing test resources")
			cmd := exec.Command("kubectl", "delete", "namespace", testNamespace, "--ignore-not-found", "--timeout=60s")
			_, _ = utils.Run(cmd)
			cmd = exec.Command("kubectl", "delete", "gateway", "test-gateway", "-n", "default", "--ignore-not-found")
			_, _ = utils.Run(cmd)

			By("installing Gateway API CRDs")
			cmd = exec.Command("kubectl", "apply", "-f",
				"https://github.com/kubernetes-sigs/gateway-api/releases/download/v1.2.0/standard-install.yaml")
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to install Gateway API CRDs")

			By("waiting for Gateway API CRDs to be established")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "crd", "gateways.gateway.networking.k8s.io")
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
			}, 2*time.Minute, time.Second).Should(Succeed())

			By("creating test namespace")
			cmd = exec.Command("kubectl", "create", "namespace", testNamespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create test namespace")

			By("labeling namespace for NIC management")
			cmd = exec.Command("kubectl", "label", "namespace", testNamespace, "nic.nebari.dev/managed=true")
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to label namespace")

			By("creating a test Gateway")
			gatewayYAML := `
apiVersion: gateway.networking.k8s.io/v1
kind: Gateway
metadata:
  name: test-gateway
  namespace: default
spec:
  gatewayClassName: test-gateway-class
  listeners:
  - name: http
    protocol: HTTP
    port: 80
    allowedRoutes:
      namespaces:
        from: All
  - name: https
    protocol: HTTPS
    port: 443
    allowedRoutes:
      namespaces:
        from: All
    tls:
      mode: Terminate
      certificateRefs:
      - name: wildcard-cert
        kind: Secret
`
			cmd = exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(gatewayYAML)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create Gateway")

			By("creating a test application deployment")
			appYAML := fmt.Sprintf(`
apiVersion: apps/v1
kind: Deployment
metadata:
  name: test-app
  namespace: %s
spec:
  replicas: 1
  selector:
    matchLabels:
      app: test-app
  template:
    metadata:
      labels:
        app: test-app
    spec:
      containers:
      - name: nginx
        image: nginx:alpine
        ports:
        - containerPort: 80
---
apiVersion: v1
kind: Service
metadata:
  name: test-app
  namespace: %s
spec:
  selector:
    app: test-app
  ports:
  - port: 80
    targetPort: 80
`, testNamespace, testNamespace)
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

			cmd = exec.Command("kubectl", "delete", "gateway", "test-gateway", "-n", "default", "--ignore-not-found")
			_, _ = utils.Run(cmd)
		})

		It("should successfully reconcile a NicApp and create HTTPRoute", func() {
			nicAppName := "test-nicapp"

			By("creating a NicApp CR")
			nicAppYAML := fmt.Sprintf(`
apiVersion: webapp.nebari.dev/v1
kind: NicApp
metadata:
  name: %s
  namespace: %s
spec:
  hostname: test-app.example.com
  service:
    name: test-app
    port: 80
  gateway:
    name: test-gateway
    namespace: default
  tls:
    enabled: false
  auth:
    enabled: false
`, nicAppName, testNamespace)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nicAppYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create NicApp")

			By("verifying HTTPRoute was created")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "httproute",
					fmt.Sprintf("%s-route", nicAppName), "-n", testNamespace)
				_, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
			}, 2*time.Minute, time.Second).Should(Succeed())

			By("verifying HTTPRoute has correct hostname")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "httproute",
					fmt.Sprintf("%s-route", nicAppName), "-n", testNamespace,
					"-o", "jsonpath={.spec.hostnames[0]}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("test-app.example.com"))
			}, 1*time.Minute, time.Second).Should(Succeed())

			By("verifying HTTPRoute references correct service")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "httproute",
					fmt.Sprintf("%s-route", nicAppName), "-n", testNamespace,
					"-o", "jsonpath={.spec.rules[0].backendRefs[0].name}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("test-app"))
			}, 1*time.Minute, time.Second).Should(Succeed())

			By("verifying NicApp status is updated")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "nicapp", nicAppName, "-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}, 2*time.Minute, time.Second).Should(Succeed())

			By("cleaning up NicApp")
			cmd = exec.Command("kubectl", "delete", "nicapp", nicAppName, "-n", testNamespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())

			By("verifying HTTPRoute is deleted")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "httproute",
					fmt.Sprintf("%s-route", nicAppName), "-n", testNamespace)
				_, err := utils.Run(cmd)
				g.Expect(err).To(HaveOccurred())
			}, 1*time.Minute, time.Second).Should(Succeed())
		})

		It("should fail validation for missing service", func() {
			nicAppName := "test-nicapp-invalid"

			By("creating a NicApp with non-existent service")
			nicAppYAML := fmt.Sprintf(`
apiVersion: webapp.nebari.dev/v1
kind: NicApp
metadata:
  name: %s
  namespace: %s
spec:
  hostname: invalid-app.example.com
  service:
    name: non-existent-service
    port: 80
  gateway:
    name: test-gateway
    namespace: default
  tls:
    enabled: false
  auth:
    enabled: false
`, nicAppName, testNamespace)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nicAppYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create NicApp")

			By("verifying NicApp has validation error condition")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "nicapp", nicAppName, "-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("False"))
			}, 2*time.Minute, time.Second).Should(Succeed())

			By("verifying error message mentions service")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "nicapp", nicAppName, "-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='Ready')].message}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(ContainSubstring("service"))
			}, 1*time.Minute, time.Second).Should(Succeed())

			By("cleaning up invalid NicApp")
			cmd = exec.Command("kubectl", "delete", "nicapp", nicAppName, "-n", testNamespace)
			_, _ = utils.Run(cmd)
		})

		It("should reconcile NicApp with TLS enabled", func() {
			nicAppName := "test-nicapp-tls"

			By("creating a wildcard TLS secret")
			secretYAML := fmt.Sprintf(`
apiVersion: v1
kind: Secret
metadata:
  name: wildcard-cert
  namespace: %s
type: kubernetes.io/tls
stringData:
  tls.crt: |
    -----BEGIN CERTIFICATE-----
    MIICEzCCAXygAwIBAgIQMIMChMLGrR+QvmQvpwAU6zANBgkqhkiG9w0BAQsFADAU
    MRIwEAYDVQQDEwloYXJib3ItY2EwHhcNMjAwMTAxMDAwMDAwWhcNMjEwMTAxMDAw
    MDAwWjAUMRIwEAYDVQQDEwloYXJib3ItY2EwgZ8wDQYJKoZIhvcNAQEBBQADgY0A
    MIGJAoGBAOjSasdasdasd+BPu5Fc4wFXyyLkkU0EyxO+D92MBGfI6Jn4KUVZhNQd
    -----END CERTIFICATE-----
  tls.key: |
    -----BEGIN RSA PRIVATE KEY-----
    MIICXAIBAAKBgQDo0mrXdsadasdasdasd+BPu5Fc4wFXyyLkkU0EyxO+D92MBGfI
    -----END RSA PRIVATE KEY-----
`, testNamespace)

			cmd := exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(secretYAML)
			_, err := utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create TLS secret")

			By("creating a NicApp with TLS enabled")
			nicAppYAML := fmt.Sprintf(`
apiVersion: webapp.nebari.dev/v1
kind: NicApp
metadata:
  name: %s
  namespace: %s
spec:
  hostname: secure-app.example.com
  service:
    name: test-app
    port: 80
  gateway:
    name: test-gateway
    namespace: default
  tls:
    enabled: true
    mode: wildcard
  auth:
    enabled: false
`, nicAppName, testNamespace)

			cmd = exec.Command("kubectl", "apply", "-f", "-")
			cmd.Stdin = strings.NewReader(nicAppYAML)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred(), "Failed to create NicApp with TLS")

			By("verifying HTTPRoute was created with HTTPS parent")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "httproute",
					fmt.Sprintf("%s-route", nicAppName), "-n", testNamespace,
					"-o", "jsonpath={.spec.parentRefs[?(@.sectionName=='https')]}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).NotTo(BeEmpty())
			}, 2*time.Minute, time.Second).Should(Succeed())

			By("verifying NicApp status shows TLS configured")
			Eventually(func(g Gomega) {
				cmd := exec.Command("kubectl", "get", "nicapp", nicAppName, "-n", testNamespace,
					"-o", "jsonpath={.status.conditions[?(@.type=='TLSConfigured')].status}")
				output, err := utils.Run(cmd)
				g.Expect(err).NotTo(HaveOccurred())
				g.Expect(output).To(Equal("True"))
			}, 2*time.Minute, time.Second).Should(Succeed())

			By("cleaning up TLS NicApp")
			cmd = exec.Command("kubectl", "delete", "nicapp", nicAppName, "-n", testNamespace)
			_, err = utils.Run(cmd)
			Expect(err).NotTo(HaveOccurred())
		})

		It("should verify reconcile metrics are reported", func() {
			By("getting metrics output")
			metricsOutput, err := getMetricsOutput()
			Expect(err).NotTo(HaveOccurred(), "Failed to retrieve metrics")

			By("verifying nicapp controller reconcile metrics exist")
			Expect(metricsOutput).To(ContainSubstring("controller_runtime_reconcile_total{controller=\"nicapp\""),
				"NicApp controller metrics not found")
		})
	})
})

// serviceAccountToken returns a token for the specified service account in the given namespace.
// It uses the Kubernetes TokenRequest API to generate a token by directly sending a request
// and parsing the resulting token from the API response.
func serviceAccountToken() (string, error) {
	const tokenRequestRawString = `{
		"apiVersion": "authentication.k8s.io/v1",
		"kind": "TokenRequest"
	}`

	// Temporary file to store the token request
	secretName := fmt.Sprintf("%s-token-request", serviceAccountName)
	tokenRequestFile := filepath.Join("/tmp", secretName)
	err := os.WriteFile(tokenRequestFile, []byte(tokenRequestRawString), os.FileMode(0o644))
	if err != nil {
		return "", err
	}

	var out string
	verifyTokenCreation := func(g Gomega) {
		// Execute kubectl command to create the token
		cmd := exec.Command("kubectl", "create", "--raw", fmt.Sprintf(
			"/api/v1/namespaces/%s/serviceaccounts/%s/token",
			namespace,
			serviceAccountName,
		), "-f", tokenRequestFile)

		output, err := cmd.CombinedOutput()
		g.Expect(err).NotTo(HaveOccurred())

		// Parse the JSON output to extract the token
		var token tokenRequest
		err = json.Unmarshal(output, &token)
		g.Expect(err).NotTo(HaveOccurred())

		out = token.Status.Token
	}
	Eventually(verifyTokenCreation).Should(Succeed())

	return out, err
}

// getMetricsOutput retrieves and returns the logs from the curl pod used to access the metrics endpoint.
func getMetricsOutput() (string, error) {
	By("getting the curl-metrics logs")
	cmd := exec.Command("kubectl", "logs", "curl-metrics", "-n", namespace)
	return utils.Run(cmd)
}

// tokenRequest is a simplified representation of the Kubernetes TokenRequest API response,
// containing only the token field that we need to extract.
type tokenRequest struct {
	Status struct {
		Token string `json:"token"`
	} `json:"status"`
}
