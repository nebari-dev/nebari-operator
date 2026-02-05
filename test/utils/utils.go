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

package utils

import (
	"bufio"
	"bytes"
	"fmt"
	"os"
	"os/exec"
	"sort"
	"strings"

	. "github.com/onsi/ginkgo/v2" // nolint:revive,staticcheck
)

const (
	defaultKindBinary  = "kind"
	defaultKindCluster = "kind"
)

// Run executes the provided command within this context
func Run(cmd *exec.Cmd) (string, error) {
	dir, _ := GetProjectDir()
	cmd.Dir = dir

	if err := os.Chdir(cmd.Dir); err != nil {
		_, _ = fmt.Fprintf(GinkgoWriter, "chdir dir: %q\n", err)
	}

	cmd.Env = append(os.Environ(), "GO111MODULE=on")
	command := strings.Join(cmd.Args, " ")
	_, _ = fmt.Fprintf(GinkgoWriter, "running: %q\n", command)
	output, err := cmd.CombinedOutput()
	if err != nil {
		return string(output), fmt.Errorf("%q failed with error %q: %w", command, string(output), err)
	}

	return string(output), nil
}

// IsCertManagerInstalled checks if cert-manager is installed by checking for its CRDs.
func IsCertManagerInstalled() bool {
	cmd := exec.Command("kubectl", "get", "crd", "certificates.cert-manager.io")
	_, err := Run(cmd)
	return err == nil
}

// IsEnvoyGatewayInstalled checks if Envoy Gateway is installed.
func IsEnvoyGatewayInstalled() bool {
	cmd := exec.Command("kubectl", "get", "deployment", "envoy-gateway", "-n", "envoy-gateway-system")
	_, err := Run(cmd)
	return err == nil
}

// IsGatewayAPIInstalled checks if Gateway API CRDs are installed.
func IsGatewayAPIInstalled() bool {
	cmd := exec.Command("kubectl", "get", "crd", "gateways.gateway.networking.k8s.io")
	_, err := Run(cmd)
	return err == nil
}

// IsGatewayReady checks if the nebari-gateway exists and is ready.
func IsGatewayReady() bool {
	cmd := exec.Command("kubectl", "get", "gateway", "nebari-gateway", "-n", "envoy-gateway-system")
	_, err := Run(cmd)
	return err == nil
}

// CreateKindCluster creates a kind cluster with the given name
func CreateKindCluster() error {
	cluster := defaultKindCluster
	if v, ok := os.LookupEnv("KIND_CLUSTER"); ok {
		cluster = v
	}
	kindBinary := defaultKindBinary
	if v, ok := os.LookupEnv("KIND"); ok {
		kindBinary = v
	}

	// Check if cluster already exists
	checkCmd := exec.Command(kindBinary, "get", "clusters")
	output, err := Run(checkCmd)
	if err == nil && strings.Contains(output, cluster) {
		_, _ = fmt.Fprintf(GinkgoWriter, "Kind cluster %q already exists, verifying it's healthy...\n", cluster)

		// Verify the cluster has nodes
		nodesCmd := exec.Command(kindBinary, "get", "nodes", "--name", cluster)
		nodesOutput, nodesErr := Run(nodesCmd)
		nodeLines := GetNonEmptyLines(nodesOutput)

		if nodesErr == nil && len(nodeLines) > 0 {
			// Double-check that Docker containers are actually running
			// Get the first node name and verify it exists as a Docker container
			nodeName := strings.TrimSpace(nodeLines[0])
			dockerCheckCmd := exec.Command("docker", "inspect", nodeName, "--format", "{{.State.Running}}")
			dockerOutput, dockerErr := Run(dockerCheckCmd)

			if dockerErr == nil && strings.TrimSpace(dockerOutput) == "true" {
				_, _ = fmt.Fprintf(GinkgoWriter,
					"Kind cluster %q is healthy with %d node(s) running, skipping creation\n",
					cluster, len(nodeLines))
				return nil
			}

			_, _ = fmt.Fprintf(GinkgoWriter,
				"Kind cluster %q has node metadata but Docker container is not running, deleting...\n",
				cluster)
		} else {
			_, _ = fmt.Fprintf(GinkgoWriter,
				"Kind cluster %q is unhealthy (no nodes found), deleting and recreating...\n",
				cluster)
		}

		// Cluster exists but is unhealthy, delete it
		deleteCmd := exec.Command(kindBinary, "delete", "cluster", "--name", cluster)
		if _, delErr := Run(deleteCmd); delErr != nil {
			_, _ = fmt.Fprintf(GinkgoWriter, "Warning: failed to delete unhealthy cluster: %v\n", delErr)
		}
	}

	// Create the cluster
	_, _ = fmt.Fprintf(GinkgoWriter, "Creating Kind cluster %q...\n", cluster)
	cmd := exec.Command(kindBinary, "create", "cluster", "--name", cluster, "--wait", "5m")
	output, err = Run(cmd)
	if err != nil {
		return fmt.Errorf("failed to create kind cluster: %w\nOutput: %s", err, output)
	}

	// Verify the cluster was created successfully
	nodesCmd := exec.Command(kindBinary, "get", "nodes", "--name", cluster)
	nodesOutput, nodesErr := Run(nodesCmd)
	if nodesErr != nil || len(GetNonEmptyLines(nodesOutput)) == 0 {
		return fmt.Errorf("kind cluster created but has no nodes. This may indicate a Docker or kind issue")
	}

	_, _ = fmt.Fprintf(GinkgoWriter, "Successfully created Kind cluster %q\n", cluster)
	return nil
}

// DeleteKindCluster deletes the kind cluster
func DeleteKindCluster() error {
	cluster := defaultKindCluster
	if v, ok := os.LookupEnv("KIND_CLUSTER"); ok {
		cluster = v
	}
	kindBinary := defaultKindBinary
	if v, ok := os.LookupEnv("KIND"); ok {
		kindBinary = v
	}
	cmd := exec.Command(kindBinary, "delete", "cluster", "--name", cluster)
	_, err := Run(cmd)
	return err
}

// LoadImageToKindClusterWithName loads a local docker image to the kind cluster
func LoadImageToKindClusterWithName(name string) error {
	cluster := defaultKindCluster
	if v, ok := os.LookupEnv("KIND_CLUSTER"); ok {
		cluster = v
	}
	kindBinary := defaultKindBinary
	if v, ok := os.LookupEnv("KIND"); ok {
		kindBinary = v
	}

	// Verify cluster still has nodes before attempting to load image
	// This prevents race conditions where cluster becomes unhealthy between checks
	_, _ = fmt.Fprintf(GinkgoWriter, "Verifying cluster health before loading image...\n")
	nodesCmd := exec.Command(kindBinary, "get", "nodes", "--name", cluster)
	nodesOutput, nodesErr := Run(nodesCmd)
	nodeLines := GetNonEmptyLines(nodesOutput)

	if nodesErr != nil {
		return fmt.Errorf("failed to get nodes for cluster %q: %w", cluster, nodesErr)
	}

	if len(nodeLines) == 0 {
		return fmt.Errorf("kind cluster %q has no nodes, cannot load image. Cluster may be in a broken state", cluster)
	}

	// Verify Docker container is actually running
	nodeName := strings.TrimSpace(nodeLines[0])
	dockerCheckCmd := exec.Command("docker", "inspect", nodeName, "--format", "{{.State.Running}}")
	dockerOutput, dockerErr := Run(dockerCheckCmd)

	if dockerErr != nil {
		return fmt.Errorf("node %q exists in kind but Docker container not found: %w", nodeName, dockerErr)
	}

	if strings.TrimSpace(dockerOutput) != "true" {
		return fmt.Errorf(
			"node %q Docker container exists but is not running (state: %s)",
			nodeName, strings.TrimSpace(dockerOutput))
	}

	_, _ = fmt.Fprintf(GinkgoWriter, "Cluster has %d healthy node(s), loading image %s...\n", len(nodeLines), name)

	kindOptions := []string{"load", "docker-image", name, "--name", cluster}
	cmd := exec.Command(kindBinary, kindOptions...)
	_, err := Run(cmd)
	return err
}

// GetNonEmptyLines converts given command output string into individual objects
// according to line breakers, and ignores the empty elements in it.
func GetNonEmptyLines(output string) []string {
	var res []string
	elements := strings.Split(output, "\n")
	for _, element := range elements {
		if element != "" {
			res = append(res, element)
		}
	}

	return res
}

// LoadTestDataFile reads a YAML file from the testdata directory and replaces placeholders.
// The replacements map contains key-value pairs where keys are placeholders to be replaced
// with their corresponding values.
func LoadTestDataFile(filename string, replacements map[string]string) (string, error) {
	projectDir, err := GetProjectDir()
	if err != nil {
		return "", fmt.Errorf("failed to get project directory: %w", err)
	}

	filePath := fmt.Sprintf("%s/test/e2e/testdata/%s", projectDir, filename)
	content, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("failed to read file %s: %w", filePath, err)
	}

	result := string(content)

	// Sort placeholders by length (descending) to avoid partial replacements
	// e.g., replace "HOSTNAME_PLACEHOLDER" before "NAME_PLACEHOLDER"
	placeholders := make([]string, 0, len(replacements))
	for placeholder := range replacements {
		placeholders = append(placeholders, placeholder)
	}
	sort.Slice(placeholders, func(i, j int) bool {
		return len(placeholders[i]) > len(placeholders[j])
	})

	for _, placeholder := range placeholders {
		result = strings.ReplaceAll(result, placeholder, replacements[placeholder])
	}

	return result, nil
}

// GetProjectDir will return the directory where the project is
func GetProjectDir() (string, error) {
	wd, err := os.Getwd()
	if err != nil {
		return wd, fmt.Errorf("failed to get current working directory: %w", err)
	}
	wd = strings.ReplaceAll(wd, "/test/e2e", "")
	return wd, nil
}

// UncommentCode searches for target in the file and remove the comment prefix
// of the target content. The target content may span multiple lines.
func UncommentCode(filename, target, prefix string) error {
	// false positive
	// nolint:gosec
	content, err := os.ReadFile(filename)
	if err != nil {
		return fmt.Errorf("failed to read file %q: %w", filename, err)
	}
	strContent := string(content)

	idx := strings.Index(strContent, target)
	if idx < 0 {
		return fmt.Errorf("unable to find the code %q to be uncomment", target)
	}

	out := new(bytes.Buffer)
	_, err = out.Write(content[:idx])
	if err != nil {
		return fmt.Errorf("failed to write to output: %w", err)
	}

	scanner := bufio.NewScanner(bytes.NewBufferString(target))
	if !scanner.Scan() {
		return nil
	}
	for {
		if _, err = out.WriteString(strings.TrimPrefix(scanner.Text(), prefix)); err != nil {
			return fmt.Errorf("failed to write to output: %w", err)
		}
		// Avoid writing a newline in case the previous line was the last in target.
		if !scanner.Scan() {
			break
		}
		if _, err = out.WriteString("\n"); err != nil {
			return fmt.Errorf("failed to write to output: %w", err)
		}
	}

	if _, err = out.Write(content[idx+len(target):]); err != nil {
		return fmt.Errorf("failed to write to output: %w", err)
	}

	// false positive
	// nolint:gosec
	if err = os.WriteFile(filename, out.Bytes(), 0644); err != nil {
		return fmt.Errorf("failed to write file %q: %w", filename, err)
	}

	return nil
}
