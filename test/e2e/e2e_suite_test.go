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
	"testing"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"

	appsv1 "github.com/nebari-dev/nebari-operator/api/v1"
	"github.com/nebari-dev/nebari-operator/test/utils"
)

var (
	// Optional Environment Variables:
	// - USE_EXISTING_CLUSTER=true: Use existing cluster instead of creating new Kind cluster
	// - SETUP_INFRASTRUCTURE=true: Run dev/scripts/services/install.sh and keycloak/setup.sh
	// - SKIP_SETUP=true: Skip all setup (cluster and infrastructure), assume everything exists
	// - SKIP_DOCKER_BUILD=true: Skip docker build, assume image is already built and loaded
	useExistingCluster  = os.Getenv("USE_EXISTING_CLUSTER") == "true"
	setupInfrastructure = os.Getenv("SETUP_INFRASTRUCTURE") == "true"
	skipSetup           = os.Getenv("SKIP_SETUP") == "true"
	skipDockerBuild     = os.Getenv("SKIP_DOCKER_BUILD") == "true"

	// Internal flags
	isKindClusterCreated = false

	// projectImage is the name of the image which will be build and loaded
	// with the code source changes to be tested.
	// Override via IMG env var (e.g. IMG=quay.io/nebari/nebari-operator:dev make test-e2e).
	projectImage = func() string {
		if v := os.Getenv("IMG"); v != "" {
			return v
		}
		return "quay.io/nebari/nebari-operator:v0.0.1"
	}()

	// k8sClient is a controller-runtime client for use in e2e tests.
	k8sClient client.Client
)

// TestE2E runs the end-to-end (e2e) test suite for the project. These tests execute in an isolated,
// temporary environment to validate project changes with the purpose of being used in CI jobs.
// The default setup requires Kind, builds/loads the Manager Docker image locally, and installs
// CertManager.
func TestE2E(t *testing.T) {
	RegisterFailHandler(Fail)
	_, _ = fmt.Fprintf(GinkgoWriter, "Starting nebari-operator integration test suite\n")
	RunSpecs(t, "e2e suite")
}

var _ = SynchronizedBeforeSuite(func() []byte {
	if !skipSetup {
		// Set cluster name for Kind utilities
		clusterName := os.Getenv("CLUSTER_NAME")
		if clusterName == "" {
			clusterName = "nebari-operator-dev"
		}
		os.Setenv("KIND_CLUSTER", clusterName)
		os.Setenv("CLUSTER_NAME", clusterName)

		// Setup cluster and infrastructure
		if !useExistingCluster {
			By("creating kind cluster via dev scripts")
			cmd := exec.Command("make", "-C", "dev", "cluster-create")
			_, err := utils.Run(cmd)
			if err == nil {
				isKindClusterCreated = true
			}
			ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to create Kind cluster")
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "Using existing cluster\n")
		}

		// Setup infrastructure (Envoy Gateway, cert-manager, Gateway, etc.)
		if setupInfrastructure {
			By("installing foundational services via dev scripts")
			cmd := exec.Command("make", "-C", "dev", "services-install")
			_, err := utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to install foundational services")

			By("configuring Keycloak realm via dev scripts")
			cmd = exec.Command("dev/scripts/services/keycloak/setup.sh")
			_, err = utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to configure Keycloak realm")
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "Skipping infrastructure setup, assuming services are already installed\n")
		}

		// Build and load operator image
		if !skipDockerBuild {
			By("building the manager(Operator) image")
			cmd := exec.Command("make", "docker-build", fmt.Sprintf("IMG=%s", projectImage))
			_, err := utils.Run(cmd)
			ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to build the manager(Operator) image")

			By("loading the manager(Operator) image on Kind")
			err = utils.LoadImageToKindClusterWithName(projectImage)
			ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to load the manager(Operator) image into Kind")
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter, "Skipping docker build, assuming image is already built and loaded\n")
		}

		// Install CRDs
		By("installing CRDs")
		cmd := exec.Command("make", "install")
		_, err := utils.Run(cmd)
		ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to install CRDs")

		// Clean up any existing deployment
		By("undeploying any existing operator")
		cmd = exec.Command("make", "undeploy")
		_, _ = utils.Run(cmd) // ignore error if nothing is deployed

		By("waiting for operator namespace to be fully removed")
		Eventually(func() error {
			cmd := exec.Command("kubectl", "get", "namespace", "nebari-operator-system")
			_, err := utils.Run(cmd)
			return err
		}, 5*time.Minute, time.Second).Should(HaveOccurred(), "Timed out waiting for nebari-operator-system namespace to be removed")

		// Deploy the operator
		By("deploying the operator")
		cmd = exec.Command("make", "deploy", fmt.Sprintf("IMG=%s", projectImage))
		_, err = utils.Run(cmd)
		ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to deploy the operator")

		By("waiting for controller-manager deployment to be available")
		Eventually(func(g Gomega) {
			cmd := exec.Command("kubectl", "get", "deployment", "nebari-operator-controller-manager",
				"-n", "nebari-operator-system",
				"-o", "jsonpath={.status.availableReplicas}")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).To(Equal("1"))
		}, 2*time.Minute, time.Second).Should(Succeed(), "Controller manager deployment did not become available")

		By("verifying operator logs for configuration errors")
		Eventually(func(g Gomega) {
			cmd := exec.Command("kubectl", "logs",
				"deployment/nebari-operator-controller-manager",
				"-n", "nebari-operator-system",
				"--tail=100")
			output, err := utils.Run(cmd)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(output).NotTo(ContainSubstring("failed to load config"))
			g.Expect(output).NotTo(ContainSubstring("config error"))
		}, 1*time.Minute, time.Second).Should(Succeed(), "Operator logs contain configuration errors")
	} else {
		_, _ = fmt.Fprintf(GinkgoWriter, "Skipping all setup, using existing cluster and infrastructure\n")
	}

	return nil
}, func(_ []byte) {
	By("setting up the k8s client")
	scheme := runtime.NewScheme()
	ExpectWithOffset(1, clientgoscheme.AddToScheme(scheme)).To(Succeed())
	ExpectWithOffset(1, appsv1.AddToScheme(scheme)).To(Succeed())
	cfg := ctrl.GetConfigOrDie()
	var err error
	k8sClient, err = client.New(cfg, client.Options{Scheme: scheme})
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), "Failed to create k8s client")
})

var _ = SynchronizedAfterSuite(func() {
	// Nothing to do per-process
}, func() {
	if skipSetup {
		return
	}

	By("undeploying the operator")
	cmd := exec.Command("make", "undeploy")
	if _, err := utils.Run(cmd); err != nil {
		warnError(err)
	}

	By("uninstalling CRDs")
	cmd = exec.Command("make", "uninstall")
	if _, err := utils.Run(cmd); err != nil {
		warnError(err)
	}

	// Teardown via dev scripts if we set things up
	if setupInfrastructure {
		_, _ = fmt.Fprintf(GinkgoWriter, "Uninstalling foundational services...\n")
		cmd := exec.Command("make", "-C", "dev", "services-uninstall")
		_, _ = utils.Run(cmd)
	}

	// Delete Kind cluster if we created it
	if isKindClusterCreated {
		By("deleting kind cluster")
		cmd := exec.Command("make", "-C", "dev", "cluster-delete")
		if _, err := utils.Run(cmd); err != nil {
			warnError(err)
		}
	}
})

func warnError(err error) {
	_, _ = fmt.Fprintf(GinkgoWriter, "warning: %v\n", err)
}
