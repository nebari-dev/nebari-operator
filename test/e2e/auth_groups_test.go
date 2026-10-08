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
	"regexp"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/nebari-dev/nebari-operator/test/utils"
)

const (
	kcNamespace = "keycloak"
	kcPod       = "keycloak-keycloakx-0"
	kcRealm     = "nebari"
	e2ePassword = "e2e-Passw0rd!"
)

// kcadm runs a kcadm.sh command in the Keycloak pod.
func kcadm(args ...string) (string, error) {
	full := append([]string{"exec", "-n", kcNamespace, kcPod, "--", "/opt/keycloak/bin/kcadm.sh"}, args...)
	return utils.Run(exec.Command("kubectl", full...))
}

// createUser creates an enabled user with a complete profile and a password,
// so Keycloak does not require a profile update at first login.
func createUser(username string) string {
	id, err := kcadm("create", "users", "-r", kcRealm, "-i",
		"-s", "username="+username, "-s", "enabled=true", "-s", "emailVerified=true",
		"-s", "email="+username+"@e2e.local", "-s", "firstName=E2E", "-s", "lastName="+username)
	Expect(err).NotTo(HaveOccurred())
	_, err = kcadm("set-password", "-r", kcRealm, "--username", username, "--new-password", e2ePassword)
	Expect(err).NotTo(HaveOccurred())
	return strings.TrimSpace(id)
}

func addToGroup(userID, groupID string) {
	_, err := kcadm("update", "users/"+userID+"/groups/"+groupID, "-r", kcRealm,
		"-s", "realm="+kcRealm, "-s", "userId="+userID, "-s", "groupId="+groupID, "-n")
	Expect(err).NotTo(HaveOccurred())
}

// loginScript runs testdata/oidc-login.sh in an in-cluster curl pod, so the
// in-cluster Keycloak URLs resolve, and returns the value of its RESULT= line.
// extra are the optional mode arguments of the script.
func loginScript(host, gatewayIP, username string, extra ...string) string {
	script, err := utils.LoadTestDataFile("oidc-login.sh", nil)
	Expect(err).NotTo(HaveOccurred())
	pod := fmt.Sprintf("login-%s-%d", username, time.Now().UnixNano()%100000)
	args := []string{"run", pod, "--rm", "-i", "--restart=Never",
		"--image=curlimages/curl:8.10.1", "--command", "--",
		"sh", "-s", "--", host, gatewayIP, username, e2ePassword}
	cmd := exec.Command("kubectl", append(args, extra...)...)
	cmd.Stdin = strings.NewReader(script)
	out, err := utils.Run(cmd)
	Expect(err).NotTo(HaveOccurred(), out)
	return parseResult(out)
}

var resultRe = regexp.MustCompile(`RESULT=(\S*)`)

// parseResult extracts the RESULT=<value> line, ignoring the kubectl banner
// and "pod deleted" noise that surrounds it.
func parseResult(out string) string {
	m := resultRe.FindStringSubmatch(out)
	Expect(m).NotTo(BeNil(), out)
	return m[1]
}

// loginStatus performs a real OIDC login and returns the status of the
// authenticated request.
func loginStatus(host, gatewayIP, username string) string {
	return loginScript(host, gatewayIP, username)
}

var _ = Describe("Auth groups gateway enforcement", Ordered, func() {
	const ns = "e2e-auth-groups"
	var gatewayIP string
	var userIDs []string
	var groupIDs []string

	BeforeAll(func() {
		if _, err := utils.Run(exec.Command("kubectl", "get", "namespace", kcNamespace)); err != nil {
			Skip("Keycloak not installed")
		}

		Eventually(func() string {
			out, _ := utils.Run(exec.Command("kubectl", "get", "svc", "-n", "envoy-gateway-system",
				"-l", "gateway.envoyproxy.io/owning-gateway-name=nebari-gateway",
				"-o", "jsonpath={.items[0].status.loadBalancer.ingress[0].ip}"))
			gatewayIP = strings.TrimSpace(out)
			return gatewayIP
		}, 3*time.Minute, 5*time.Second).ShouldNot(BeEmpty())

		By("authenticating kcadm")
		_, err := kcadm("config", "credentials", "--server", "http://localhost:8080/auth",
			"--realm", "master", "--user", "admin", "--password", "admin")
		Expect(err).NotTo(HaveOccurred())

		By("creating groups /e2e-allowed and /e2e-parent/e2e-child")
		allowed, err := kcadm("create", "groups", "-r", kcRealm, "-i", "-s", "name=e2e-allowed")
		Expect(err).NotTo(HaveOccurred())
		allowedID := strings.TrimSpace(allowed)
		groupIDs = append(groupIDs, allowedID)
		parent, err := kcadm("create", "groups", "-r", kcRealm, "-i", "-s", "name=e2e-parent")
		Expect(err).NotTo(HaveOccurred())
		parentID := strings.TrimSpace(parent)
		groupIDs = append(groupIDs, parentID)
		child, err := kcadm("create", "groups/"+parentID+"/children", "-r", kcRealm, "-i", "-s", "name=e2e-child")
		Expect(err).NotTo(HaveOccurred())
		childGroupID := strings.TrimSpace(child)

		By("creating users")
		inID := createUser("e2e-in")
		userIDs = append(userIDs, inID)
		outID := createUser("e2e-out")
		userIDs = append(userIDs, outID)
		childID := createUser("e2e-child")
		userIDs = append(userIDs, childID)
		addToGroup(inID, allowedID)
		addToGroup(childID, childGroupID)
		// e2e-out is in no groups, so its token has no groups claim.

		SetupTestNamespace(ns)
		DeployTestApp(ns)
	})

	AfterAll(func() {
		// Only the users and groups created by BeforeAll are tracked by ID.
		for _, id := range userIDs {
			_, _ = kcadm("delete", "users/"+id, "-r", kcRealm)
		}
		for _, id := range groupIDs {
			_, _ = kcadm("delete", "groups/"+id, "-r", kcRealm)
		}
		CleanupTestNamespace(ns)
	})

	applyApp := func(name, host, group string) {
		yaml := fmt.Sprintf(`
apiVersion: reconcilers.nebari.dev/v1
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
  auth:
    enabled: true
    provider: keycloak
    groups: [%q]
`, name, ns, host, group)
		cmd := exec.Command("kubectl", "apply", "-f", "-")
		cmd.Stdin = strings.NewReader(yaml)
		_, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred())
		Eventually(func(g Gomega) {
			out, err := utils.Run(exec.Command("kubectl", "get", "nebariapp", name, "-n", ns,
				"-o", "jsonpath={.status.conditions[?(@.type=='AuthReady')].status}"))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(out).To(Equal("True"))
		}, 5*time.Minute, 5*time.Second).Should(Succeed())
		// Give Envoy time to receive the updated xDS config.
		time.Sleep(10 * time.Second)
	}

	It("admits members and denies everyone else", func() {
		name := "groups-allowed"
		host := "groups-allowed.nebari.local"
		applyApp(name, host, "/e2e-allowed")

		By("an unauthenticated request is redirected to Keycloak")
		out, err := utils.Run(exec.Command("kubectl", "run", fmt.Sprintf("anon-%d", time.Now().UnixNano()%100000),
			"--rm", "-i", "--restart=Never", "--image=curlimages/curl:8.10.1", "--command", "--",
			"curl", "-sk", "-o", "/dev/null", "-w", "RESULT=%{http_code}\\n",
			"--resolve", fmt.Sprintf("%s:443:%s", host, gatewayIP), "https://"+host+"/"))
		Expect(err).NotTo(HaveOccurred())
		Expect(parseResult(out)).To(Equal("302"))

		Expect(loginStatus(host, gatewayIP, "e2e-in")).To(Equal("200"), "member of /e2e-allowed")
		Expect(loginStatus(host, gatewayIP, "e2e-out")).To(Equal("403"), "user with no groups claim")
		Expect(loginStatus(host, gatewayIP, "e2e-child")).To(Equal("403"), "member of a different group")

		By("a non-member cannot borrow a member's access-token cookie")
		cookie := fmt.Sprintf("nebari-at-%s-%s", ns, name)
		memberToken := loginScript(host, gatewayIP, "e2e-in", "print-cookie", cookie)
		Expect(memberToken).NotTo(BeEmpty())
		forged := loginScript(host, gatewayIP, "e2e-out", "forge", cookie, memberToken)
		_, _ = fmt.Fprintf(GinkgoWriter, "forged-cookie status: %s\n", forged)
		Expect(forged).NotTo(Equal("200"), "e2e-out session with e2e-in access-token cookie")
	})

	It("does not admit subgroup members through the parent", func() {
		host := "groups-parent.nebari.local"
		applyApp("groups-parent", host, "/e2e-parent")
		Expect(loginStatus(host, gatewayIP, "e2e-child")).To(Equal("403"), "member of /e2e-parent/e2e-child only")
	})
})
