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

	"github.com/nebari-dev/nebari-operator/test/utils"
)

// DiagnosticInfo collects comprehensive diagnostic information about a resource
// for debugging test failures. This function gathers:
// - Resource YAML
// - Resource events
// - Related pod status (for deployments)
// - Operator logs (if applicable)
func DiagnosticInfo(namespace, resourceType, resourceName string) string {
	diagnostics := fmt.Sprintf("\n=== %s/%s Diagnostic Information ===\n", resourceType, resourceName)

	// Get resource YAML
	cmd := exec.Command("kubectl", "get", resourceType, resourceName, "-n", namespace, "-o", "yaml")
	if output, err := utils.Run(cmd); err == nil {
		diagnostics += fmt.Sprintf("\n--- Resource YAML ---\n%s\n", output)
	} else {
		diagnostics += fmt.Sprintf("\n--- Resource YAML ---\nError: %v\n", err)
	}

	// Get resource-specific events
	cmd = exec.Command("kubectl", "get", "events", "-n", namespace,
		"--field-selector", fmt.Sprintf("involvedObject.name=%s", resourceName),
		"--sort-by=.lastTimestamp")
	if output, err := utils.Run(cmd); err == nil && output != "" {
		diagnostics += fmt.Sprintf("\n--- Resource Events ---\n%s\n", output)
	}

	// Get all namespace events (broader context)
	cmd = exec.Command("kubectl", "get", "events", "-n", namespace,
		"--sort-by=.lastTimestamp", "--limit=20")
	if output, err := utils.Run(cmd); err == nil && output != "" {
		diagnostics += fmt.Sprintf("\n--- Recent Namespace Events ---\n%s\n", output)
	}

	// For NebariApp resources, include related HTTPRoute and SecurityPolicy
	if resourceType == "nebariapp" {
		// Check HTTPRoute
		routeName := resourceName + "-route"
		cmd = exec.Command("kubectl", "get", "httproute", routeName, "-n", namespace, "-o", "yaml")
		if output, err := utils.Run(cmd); err == nil {
			diagnostics += fmt.Sprintf("\n--- Related HTTPRoute ---\n%s\n", output)
		} else {
			diagnostics += fmt.Sprintf("\n--- Related HTTPRoute ---\nNot found or error: %v\n", err)
		}

		// Check SecurityPolicy
		cmd = exec.Command("kubectl", "get", "securitypolicy", "-n", namespace,
			"-l", fmt.Sprintf("nebari.dev/app=%s", resourceName), "-o", "yaml")
		if output, err := utils.Run(cmd); err == nil && !strings.Contains(output, "No resources found") {
			diagnostics += fmt.Sprintf("\n--- Related SecurityPolicy ---\n%s\n", output)
		}
	}

	// For Deployments, include pod details
	if resourceType == "deployment" {
		cmd = exec.Command("kubectl", "get", "pods", "-n", namespace,
			"-l", fmt.Sprintf("app=%s", resourceName), "-o", "wide")
		if output, err := utils.Run(cmd); err == nil {
			diagnostics += fmt.Sprintf("\n--- Related Pods ---\n%s\n", output)
		}

		cmd = exec.Command("kubectl", "describe", "pods", "-n", namespace,
			"-l", fmt.Sprintf("app=%s", resourceName))
		if output, err := utils.Run(cmd); err == nil {
			diagnostics += fmt.Sprintf("\n--- Pod Details ---\n%s\n", output)
		}
	}

	// Include operator logs (last 50 lines)
	cmd = exec.Command("kubectl", "logs", "-n", "nebari-operator-system",
		"-l", "control-plane=controller-manager",
		"--tail=50", "--timestamps")
	if output, err := utils.Run(cmd); err == nil {
		diagnostics += fmt.Sprintf("\n--- Operator Logs (last 50 lines) ---\n%s\n", output)
	}

	diagnostics += "\n=== End Diagnostics ===\n"
	return diagnostics
}

// DeploymentDiagnostics is a specialized version for deployment failures
// Includes additional pod-level information
func DeploymentDiagnostics(namespace, deploymentName string) string {
	diagnostics := fmt.Sprintf("\n=== Deployment Diagnostic Information ===\n")

	// Deployment status
	cmd := exec.Command("kubectl", "get", "deployment", deploymentName, "-n", namespace, "-o", "yaml")
	if output, err := utils.Run(cmd); err == nil {
		diagnostics += fmt.Sprintf("\nDeployment YAML:\n%s\n", output)
	}

	// Pod listing
	cmd = exec.Command("kubectl", "get", "pods", "-n", namespace, "-l", fmt.Sprintf("app=%s", deploymentName))
	if output, err := utils.Run(cmd); err == nil {
		diagnostics += fmt.Sprintf("\nPod Status:\n%s\n", output)
	}

	// Pod descriptions
	cmd = exec.Command("kubectl", "describe", "pods", "-n", namespace, "-l", fmt.Sprintf("app=%s", deploymentName))
	if output, err := utils.Run(cmd); err == nil {
		diagnostics += fmt.Sprintf("\nPod Details:\n%s\n", output)
	}

	// ReplicaSet status
	cmd = exec.Command("kubectl", "get", "replicaset", "-n", namespace,
		"-l", fmt.Sprintf("app=%s", deploymentName), "-o", "yaml")
	if output, err := utils.Run(cmd); err == nil {
		diagnostics += fmt.Sprintf("\nReplicaSet Details:\n%s\n", output)
	}

	// Namespace events
	cmd = exec.Command("kubectl", "get", "events", "-n", namespace, "--sort-by=.lastTimestamp")
	if output, err := utils.Run(cmd); err == nil {
		diagnostics += fmt.Sprintf("\nNamespace Events:\n%s\n", output)
	}

	// Node resources (check if scheduling issues)
	cmd = exec.Command("kubectl", "top", "nodes")
	if output, err := utils.Run(cmd); err == nil {
		diagnostics += fmt.Sprintf("\nNode Resources:\n%s\n", output)
	}

	return diagnostics
}

// NebariAppDiagnostics provides comprehensive diagnostics for NebariApp failures
func NebariAppDiagnostics(namespace, appName string) string {
	return DiagnosticInfo(namespace, "nebariapp", appName)
}

// HTTPRouteDiagnostics provides diagnostics for HTTPRoute issues
func HTTPRouteDiagnostics(namespace, routeName string) string {
	diagnostics := DiagnosticInfo(namespace, "httproute", routeName)

	// Also check parent Gateway status
	cmd := exec.Command("kubectl", "get", "gateway", "-n", "envoy-gateway-system", "-o", "yaml")
	if output, err := utils.Run(cmd); err == nil {
		diagnostics += fmt.Sprintf("\n--- Gateway Status ---\n%s\n", output)
	}

	return diagnostics
}
