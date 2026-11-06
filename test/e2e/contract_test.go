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
	"fmt"
	"os/exec"
	"reflect"
	"sort"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/yaml"

	"github.com/nebari-dev/nebari-operator/test/utils"
)

// The contract matrix checks the operator from the seat of a pack and its
// users: sample NebariApps shaped like real packs, Keycloak personas signing
// in through a real browser flow, and, per app, an expect.yaml that says what
// each persona gets on each path and which claims the app receives. A change
// in behavior shows up as a failing cell, and fixing it means editing
// expect.yaml, so the change is reviewed like a golden file.
// testdata/contract/README.md describes the format.

const contractNamespace = "e2e-contract"

// contractPersona is a user the matrix signs in as. An empty username is
// the anonymous persona.
type contractPersona struct {
	name     string
	username string
}

var contractPersonas = []contractPersona{
	{name: "anonymous"},
	{name: "no-group", username: "ct-no-group"},
	{name: "team-a", username: "ct-team-a"},
	{name: "team-a-sub", username: "ct-team-a-sub"},
	{name: "team-b", username: "ct-team-b"},
}

// contractApp is a sample app under testdata/contract/<name>/. workload is
// true when the app ships its own workload.yaml instead of using claims-echo.
type contractApp struct {
	name     string
	workload bool
}

var contractApps = []contractApp{
	{name: "gateway-signin"},
	{name: "group-gated"},
	{name: "group-paths"},
	{name: "app-login", workload: true},
}

type contractExpect struct {
	Paths  map[string]map[string]any `json:"paths"`
	Claims *struct {
		Path     string                    `json:"path"`
		Personas map[string]map[string]any `json:"personas"`
	} `json:"claims"`
}

// loadedContractApp is a sample app with its manifest and expectations read.
type loadedContractApp struct {
	contractApp
	host     string
	manifest string
	expect   contractExpect
}

// loginPath is the first path where an anonymous request is sent to sign in.
func (a loadedContractApp) loginPath() string {
	for _, path := range sortedKeys(a.expect.Paths) {
		if fmt.Sprint(a.expect.Paths[path]["anonymous"]) == "login" {
			return path
		}
	}
	return sortedKeys(a.expect.Paths)[0]
}

func loadContractApp(app contractApp) loadedContractApp {
	vars := map[string]string{"NAMESPACE_PLACEHOLDER": contractNamespace}
	manifest, err := utils.LoadTestDataFile("contract/"+app.name+"/nebariapp.yaml", vars)
	Expect(err).NotTo(HaveOccurred())
	var nebariApp struct {
		Spec struct {
			Hostname string `json:"hostname"`
		} `json:"spec"`
	}
	Expect(yaml.Unmarshal([]byte(manifest), &nebariApp)).To(Succeed())

	raw, err := utils.LoadTestDataFile("contract/"+app.name+"/expect.yaml", nil)
	Expect(err).NotTo(HaveOccurred())
	var expect contractExpect
	Expect(yaml.UnmarshalStrict([]byte(raw), &expect)).To(Succeed(), "contract/%s/expect.yaml", app.name)
	Expect(expect.Paths).NotTo(BeEmpty(), "contract/%s/expect.yaml lists no paths", app.name)

	return loadedContractApp{contractApp: app, host: nebariApp.Spec.Hostname, manifest: manifest, expect: expect}
}

// contractResult is what the probe saw: cells[app][persona][path] is an
// outcome, claims[app][persona] the claims the app received.
type contractResult struct {
	cells  map[string]map[string]map[string]string
	claims map[string]map[string]map[string]any
	errors []string
}

func parseProbeOutput(out string) contractResult {
	r := contractResult{
		cells:  map[string]map[string]map[string]string{},
		claims: map[string]map[string]map[string]any{},
	}
	for _, line := range strings.Split(out, "\n") {
		f := strings.SplitN(strings.TrimSpace(line), " ", 4)
		if len(f) < 4 {
			continue
		}
		app, persona, rest := f[1], f[2], f[3]
		switch f[0] {
		case "CELL":
			path, outcome, _ := strings.Cut(rest, " ")
			if r.cells[app] == nil {
				r.cells[app] = map[string]map[string]string{}
			}
			if r.cells[app][persona] == nil {
				r.cells[app][persona] = map[string]string{}
			}
			r.cells[app][persona][path] = outcome
		case "CLAIMS":
			var body struct {
				Claims map[string]any `json:"claims"`
			}
			if err := json.Unmarshal([]byte(rest), &body); err != nil {
				r.errors = append(r.errors, fmt.Sprintf("%s %s: response is not the claims echo: %s", app, persona, rest))
				continue
			}
			if r.claims[app] == nil {
				r.claims[app] = map[string]map[string]any{}
			}
			r.claims[app][persona] = body.Claims
		case "ERROR":
			r.errors = append(r.errors, fmt.Sprintf("%s %s: %s", app, persona, rest))
		}
	}
	return r
}

// accessMismatches compares the cells of the probed personas with each app's
// expect.yaml. Every probed persona must be listed for every path, so adding
// a persona makes each app state what that persona gets.
func accessMismatches(apps []loadedContractApp, personas []contractPersona, r contractResult) []string {
	var out []string
	for _, app := range apps {
		for _, path := range sortedKeys(app.expect.Paths) {
			want := app.expect.Paths[path]
			for _, p := range personas {
				got := r.cells[app.name][p.name][path]
				exp, listed := want[p.name]
				switch {
				case !listed:
					out = append(out, fmt.Sprintf("%s %s %s: not in expect.yaml, got %s", app.name, path, p.name, got))
				case fmt.Sprint(exp) != got:
					out = append(out, fmt.Sprintf("%s %s %s: expected %v, got %s", app.name, path, p.name, exp, got))
				}
			}
			for name := range want {
				if !hasPersona(contractPersonas, name) {
					out = append(out, fmt.Sprintf("%s %s: expect.yaml names unknown persona %q", app.name, path, name))
				}
			}
		}
	}
	return out
}

// claimMismatches compares the claims each app received with expect.yaml.
// Only the listed claims are compared; null means the claim must be absent.
func claimMismatches(apps []loadedContractApp, personas []contractPersona, r contractResult) []string {
	var out []string
	for _, app := range apps {
		if app.expect.Claims == nil {
			continue
		}
		for _, name := range sortedKeys(app.expect.Claims.Personas) {
			if !hasPersona(personas, name) {
				out = append(out, fmt.Sprintf("%s claims: expect.yaml names unknown persona %q", app.name, name))
				continue
			}
			got, ok := r.claims[app.name][name]
			if !ok || got == nil {
				out = append(out, fmt.Sprintf("%s claims %s: the app received no access token", app.name, name))
				continue
			}
			want := app.expect.Claims.Personas[name]
			for _, claim := range sortedKeys(want) {
				value, present := got[claim]
				switch {
				case want[claim] == nil && present:
					out = append(out, fmt.Sprintf("%s claims %s %s: expected absent, got %s", app.name, name, claim, jsonText(value)))
				case want[claim] != nil && !present:
					out = append(out, fmt.Sprintf("%s claims %s %s: expected %s, got absent",
						app.name, name, claim, jsonText(want[claim])))
				case want[claim] != nil && !reflect.DeepEqual(want[claim], value):
					out = append(out, fmt.Sprintf("%s claims %s %s: expected %s, got %s",
						app.name, name, claim, jsonText(want[claim]), jsonText(value)))
				}
			}
		}
	}
	return out
}

func hasPersona(personas []contractPersona, name string) bool {
	for _, p := range personas {
		if p.name == name {
			return true
		}
	}
	return false
}

func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func jsonText(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

// runContractProbe runs testdata/contract/probe.sh in a one-off curl pod and
// parses what it printed.
func runContractProbe(gatewayIP string, apps []loadedContractApp, personas []contractPersona) (contractResult, error) {
	script, err := utils.LoadTestDataFile("contract/probe.sh", nil)
	Expect(err).NotTo(HaveOccurred())
	args := []string{"run", fmt.Sprintf("contract-probe-%d", time.Now().UnixNano()%100000),
		"-n", contractNamespace, "--rm", "-i", "--restart=Never",
		"--image=curlimages/curl:8.10.1", "--command", "--",
		"sh", "-s", "--", gatewayIP, e2ePassword}
	for _, app := range apps {
		claimsPath := "-"
		if app.expect.Claims != nil {
			claimsPath = app.expect.Claims.Path
		}
		paths := strings.Join(sortedKeys(app.expect.Paths), ",")
		for _, p := range personas {
			username := p.username
			if username == "" {
				username = "-"
			}
			personaClaims := "-"
			if username != "-" {
				personaClaims = claimsPath
			}
			args = append(args, strings.Join([]string{
				app.name, app.host, p.name, username, app.loginPath(), personaClaims, paths}, "|"))
		}
	}
	cmd := exec.Command("kubectl", args...)
	cmd.Stdin = strings.NewReader(script)
	out, err := utils.Run(cmd)
	if err != nil {
		return contractResult{}, fmt.Errorf("%w: %s", err, out)
	}
	return parseProbeOutput(out), nil
}

// formatMatrix renders what the probe saw, one row per app and path, for
// the test log.
func formatMatrix(apps []loadedContractApp, personas []contractPersona, r contractResult) string {
	var b strings.Builder
	for _, app := range apps {
		for _, path := range sortedKeys(app.expect.Paths) {
			fmt.Fprintf(&b, "%-16s %-14s", app.name, path)
			for _, p := range personas {
				fmt.Fprintf(&b, " %s=%s", p.name, r.cells[app.name][p.name][path])
			}
			b.WriteString("\n")
		}
	}
	return b.String()
}

var _ = Describe("Pack contract matrix", Ordered, func() {
	var gatewayIP string
	var apps []loadedContractApp
	var userIDs, groupIDs []string

	// deleteGroupByPath removes a group left over from an interrupted run.
	deleteGroupByPath := func(path string) {
		out, err := kcadm("get", "group-by-path"+path, "-r", kcRealm, "--fields", "id", "--format", "csv", "--noquotes")
		if err == nil && strings.TrimSpace(out) != "" {
			_, _ = kcadm("delete", "groups/"+strings.TrimSpace(out), "-r", kcRealm)
		}
	}
	deleteUserByName := func(username string) {
		out, err := kcadm("get", "users", "-r", kcRealm, "-q", "exact=true", "-q", "username="+username,
			"--fields", "id", "--format", "csv", "--noquotes")
		if err == nil && strings.TrimSpace(out) != "" {
			_, _ = kcadm("delete", "users/"+strings.TrimSpace(out), "-r", kcRealm)
		}
	}
	createGroup := func(parentID, name string) string {
		target := "groups"
		if parentID != "" {
			target = "groups/" + parentID + "/children"
		}
		id, err := kcadm("create", target, "-r", kcRealm, "-i", "-s", "name="+name)
		Expect(err).NotTo(HaveOccurred(), id)
		return strings.TrimSpace(id)
	}
	kubectlApply := func(manifest string) {
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(manifest)
		out, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), out)
	}
	waitForRollout := func(name string) {
		_, err := utils.Run(exec.Command("kubectl", "rollout", "status", "deployment/"+name,
			"-n", contractNamespace, "--timeout=3m"))
		Expect(err).NotTo(HaveOccurred(), DeploymentDiagnostics(contractNamespace, name))
	}
	waitForCondition := func(name, condition string) {
		Eventually(func(g Gomega) {
			out, err := utils.Run(exec.Command("kubectl", "get", "nebariapp", name, "-n", contractNamespace,
				"-o", fmt.Sprintf("jsonpath={.status.conditions[?(@.type=='%s')].status}", condition)))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(out).To(Equal("True"))
		}, VeryLongTimeout, 5*time.Second).Should(Succeed(), func() string {
			return NebariAppDiagnostics(contractNamespace, name)
		})
	}

	BeforeAll(func() {
		if _, err := utils.Run(exec.Command("kubectl", "get", "namespace", kcNamespace)); err != nil {
			Skip("Keycloak not installed")
		}
		if _, err := utils.Run(exec.Command("kubectl", "get", "gateway", "nebari-gateway",
			"-n", "envoy-gateway-system")); err != nil {
			Skip("nebari-gateway not installed")
		}
		Eventually(func() string {
			out, _ := utils.Run(exec.Command("kubectl", "get", "svc", "-n", "envoy-gateway-system",
				"-l", "gateway.envoyproxy.io/owning-gateway-name=nebari-gateway",
				"-o", "jsonpath={.items[0].status.loadBalancer.ingress[0].ip}"))
			gatewayIP = strings.TrimSpace(out)
			return gatewayIP
		}, LongTimeout, 5*time.Second).ShouldNot(BeEmpty())

		for _, app := range contractApps {
			apps = append(apps, loadContractApp(app))
		}

		By("seeding the personas in Keycloak")
		_, err := kcadm("config", "credentials", "--server", "http://localhost:8080/auth",
			"--realm", "master", "--user", "admin", "--password", "admin")
		Expect(err).NotTo(HaveOccurred())
		for _, p := range contractPersonas {
			if p.username != "" {
				deleteUserByName(p.username)
			}
		}
		deleteGroupByPath("/ct-team-a")
		deleteGroupByPath("/ct-team-b")
		teamA := createGroup("", "ct-team-a")
		teamASub := createGroup(teamA, "sub")
		teamB := createGroup("", "ct-team-b")
		groupIDs = append(groupIDs, teamA, teamB)
		memberOf := map[string]string{"team-a": teamA, "team-a-sub": teamASub, "team-b": teamB}
		for _, p := range contractPersonas {
			if p.username == "" {
				continue
			}
			id := createUser(p.username)
			userIDs = append(userIDs, id)
			if group, ok := memberOf[p.name]; ok {
				addToGroup(id, group)
			}
		}

		By("deploying the sample apps")
		SetupTestNamespace(contractNamespace)
		echo, err := utils.LoadTestDataFile("contract/claims-echo.yaml",
			map[string]string{"NAMESPACE_PLACEHOLDER": contractNamespace})
		Expect(err).NotTo(HaveOccurred())
		kubectlApply(echo)
		waitForRollout("claims-echo")
		// Applied together, as Helm would: the operator provisions the client
		// only once the Service exists, and the workload's pod starts once
		// the client Secret does.
		for _, app := range apps {
			kubectlApply(app.manifest)
			if app.workload {
				workload, err := utils.LoadTestDataFile("contract/"+app.name+"/workload.yaml",
					map[string]string{"NAMESPACE_PLACEHOLDER": contractNamespace})
				Expect(err).NotTo(HaveOccurred())
				kubectlApply(workload)
			}
		}
		for _, app := range apps {
			waitForCondition(app.name, "RoutingReady")
			waitForCondition(app.name, "AuthReady")
			if app.workload {
				waitForRollout(app.name)
			}
		}

		By("waiting for the gateway to serve every app as configured")
		// An anonymous request matching its row means the route and policy
		// reached Envoy. Until then a protected path can answer 200 or 404.
		anonymous := contractPersonas[:1]
		Eventually(func(g Gomega) {
			r, err := runContractProbe(gatewayIP, apps, anonymous)
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(r.errors).To(BeEmpty())
			g.Expect(accessMismatches(apps, anonymous, r)).To(BeEmpty(), formatMatrix(apps, anonymous, r))
		}, LongTimeout, 10*time.Second).Should(Succeed())
	})

	AfterAll(func() {
		for _, id := range userIDs {
			_, _ = kcadm("delete", "users/"+id, "-r", kcRealm)
		}
		for _, id := range groupIDs {
			_, _ = kcadm("delete", "groups/"+id, "-r", kcRealm)
		}
		CleanupTestNamespace(contractNamespace)
	})

	It("gives every persona what each app's expect.yaml says", func() {
		r, err := runContractProbe(gatewayIP, apps, contractPersonas)
		Expect(err).NotTo(HaveOccurred())
		_, _ = fmt.Fprintf(GinkgoWriter, "contract matrix:\n%s", formatMatrix(apps, contractPersonas, r))
		Expect(r.errors).To(BeEmpty(), "the probe could not complete")

		mismatches := accessMismatches(apps, contractPersonas, r)
		mismatches = append(mismatches, claimMismatches(apps, contractPersonas, r)...)
		Expect(mismatches).To(BeEmpty(),
			"behavior differs from test/e2e/testdata/contract/*/expect.yaml. If the change is intended, "+
				"update expect.yaml and say so in the PR.\n%s", strings.Join(mismatches, "\n"))
	})
})
