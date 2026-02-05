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

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/nebari-dev/nebari-operator/test/utils"
)

var (
	// Optional Environment Variables:
	// - USE_EXISTING_CLUSTER=true: Use existing cluster instead of creating new Kind cluster
	// - SETUP_INFRASTRUCTURE=true: Run dev/install-services.sh to setup infrastructure
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
	projectImage = "quay.io/nebari/nebari-operator:v0.0.1"
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

var _ = BeforeSuite(func() {
	if skipSetup {
		_, _ = fmt.Fprintf(GinkgoWriter, "Skipping all setup, using existing cluster and infrastructure\n")
		return
	}

	// Set cluster name for Kind utilities
	clusterName := os.Getenv("CLUSTER_NAME")
	if clusterName == "" {
		clusterName = "nic-operator-dev"
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
})

var _ = AfterSuite(func() {
	if skipSetup {
		return
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
