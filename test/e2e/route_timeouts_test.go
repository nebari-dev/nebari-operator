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
	"strconv"
	"strings"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	"github.com/nebari-dev/nebari-operator/test/utils"
)

const (
	// gatewayDefaultTimeout is Envoy's default route timeout, which Envoy
	// Gateway keeps unless a policy changes it. Routes without timeouts get it.
	gatewayDefaultTimeout = 15 * time.Second
	// routeTimeout is the request timeout the specs set on their timed routes.
	routeTimeout = 40 * time.Second
)

// timedRequest is one request for timed-requests.sh.
type timedRequest struct {
	label, host, path string
}

// timedResult is what the gateway answered to one timedRequest.
type timedResult struct {
	code     string
	seconds  float64
	lines    int
	location string
}

// expectTimedOut checks that the gateway gave up on the request with a 504
// once the given timeout had passed, and not much later.
func expectTimedOut(r timedResult, timeout time.Duration, label string) {
	ExpectWithOffset(1, r.code).To(Equal("504"), label)
	ExpectWithOffset(1, r.seconds).To(BeNumerically(">=", timeout.Seconds()-1), label)
	ExpectWithOffset(1, r.seconds).To(BeNumerically("<", timeout.Seconds()+5), label)
}

// applyYAML applies a manifest from a string.
func applyYAML(manifest string) {
	cmd := exec.Command("kubectl", "apply", "-f", "-")
	cmd.Stdin = strings.NewReader(manifest)
	out, err := utils.Run(cmd)
	ExpectWithOffset(1, err).NotTo(HaveOccurred(), out)
}

var _ = Describe("Route timeouts", Ordered, func() {
	const ns = "e2e-route-timeouts"
	var gatewayIP string
	var userIDs []string

	// startCurlPod starts a long-lived curl pod that the specs send their
	// requests from, so requests run inside the cluster next to the gateway.
	startCurlPod := func() {
		_, err := utils.Run(exec.Command("kubectl", "run", "curl", "-n", ns,
			"--image=curlimages/curl:8.10.1", "--restart=Never", "--command", "--", "sleep", "1800"))
		Expect(err).NotTo(HaveOccurred())
		_, err = utils.Run(exec.Command("kubectl", "wait", "pod/curl", "-n", ns,
			"--for=condition=Ready", "--timeout=2m"))
		Expect(err).NotTo(HaveOccurred())
	}

	// sendTimed runs testdata/timed-requests.sh in the curl pod, which sends
	// every request at once, and returns the results by label. jar is a cookie
	// jar in the pod, or "-" for anonymous requests.
	sendTimed := func(jar string, requests ...timedRequest) map[string]timedResult {
		script, err := utils.LoadTestDataFile("timed-requests.sh", nil)
		Expect(err).NotTo(HaveOccurred())
		args := []string{"exec", "-i", "-n", ns, "curl", "--", "sh", "-s", "--", gatewayIP, jar}
		for _, r := range requests {
			args = append(args, r.label, r.host, r.path)
		}
		cmd := exec.Command("kubectl", args...)
		cmd.Stdin = strings.NewReader(script)
		out, err := utils.Run(cmd)
		Expect(err).NotTo(HaveOccurred(), out)
		_, _ = fmt.Fprintf(GinkgoWriter, "timed requests:\n%s\n", out)

		results := map[string]timedResult{}
		for _, line := range strings.Split(out, "\n") {
			fields := strings.Fields(line)
			if len(fields) < 5 || fields[0] != "RESULT" {
				continue
			}
			seconds, err := strconv.ParseFloat(fields[3], 64)
			Expect(err).NotTo(HaveOccurred(), line)
			lines, err := strconv.Atoi(fields[4])
			Expect(err).NotTo(HaveOccurred(), line)
			result := timedResult{code: fields[2], seconds: seconds, lines: lines}
			if len(fields) > 5 {
				result.location = fields[5]
			}
			results[fields[1]] = result
		}
		for _, r := range requests {
			Expect(results).To(HaveKey(r.label), out)
		}
		return results
	}

	// routeRules returns the generated HTTPRoute's rules as "<path type> <path>
	// timeouts=<timeouts>" lines.
	routeRules := func(route string) []string {
		out, err := utils.Run(exec.Command("kubectl", "get", "httproute", route, "-n", ns, "-o",
			`jsonpath={range .spec.rules[*]}{range .matches[*]}{.path.type} {.path.value} {end}timeouts={.timeouts}{"\n"}{end}`))
		Expect(err).NotTo(HaveOccurred())
		return strings.Split(strings.TrimSpace(out), "\n")
	}

	// waitForCondition waits for a NebariApp condition to be True.
	waitForCondition := func(name, condition string) {
		EventuallyWithOffset(1, func(g Gomega) {
			out, err := utils.Run(exec.Command("kubectl", "get", "nebariapp", name, "-n", ns,
				"-o", fmt.Sprintf("jsonpath={.status.conditions[?(@.type=='%s')].status}", condition)))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(out).To(Equal("True"))
		}, 5*time.Minute, 5*time.Second).Should(Succeed(),
			func() string { return ConnectivityDiagnostics(ns, name) })
	}

	// waitForRoute waits until Envoy Gateway has accepted the HTTPRoute and the
	// gateway answers a quick request on the given path, so the route's config
	// has reached Envoy before a spec times anything.
	waitForRoute := func(route, host, path string) {
		EventuallyWithOffset(1, func(g Gomega) {
			out, err := utils.Run(exec.Command("kubectl", "get", "httproute", route, "-n", ns,
				"-o", "jsonpath={.status.parents[0].conditions[?(@.type=='Accepted')].status}"))
			g.Expect(err).NotTo(HaveOccurred())
			g.Expect(out).To(Equal("True"))
		}, 5*time.Minute, 5*time.Second).Should(Succeed())
		EventuallyWithOffset(1, func(g Gomega) {
			r := sendTimed("-", timedRequest{"ready", host, path})["ready"]
			g.Expect(r.code).To(Equal("200"))
		}, 3*time.Minute, 5*time.Second).Should(Succeed())
	}

	BeforeAll(func() {
		if _, err := utils.Run(exec.Command("kubectl", "get", "gateway", "nebari-gateway",
			"-n", "envoy-gateway-system")); err != nil {
			Skip("Gateway 'nebari-gateway' not found - run 'make setup' in dev/ first")
		}
		Eventually(func() string {
			out, _ := utils.Run(exec.Command("kubectl", "get", "svc", "-n", "envoy-gateway-system",
				"-l", "gateway.envoyproxy.io/owning-gateway-name=nebari-gateway",
				"-o", "jsonpath={.items[0].status.loadBalancer.ingress[0].ip}"))
			gatewayIP = strings.TrimSpace(out)
			return gatewayIP
		}, 3*time.Minute, 5*time.Second).ShouldNot(BeEmpty())

		SetupTestNamespace(ns)

		By("deploying the slow backend, directly and behind a reverse-proxy sidecar")
		manifest, err := utils.LoadTestDataFile("slow-app.yaml", map[string]string{"NAMESPACE_PLACEHOLDER": ns})
		Expect(err).NotTo(HaveOccurred())
		applyYAML(manifest)
		for _, deployment := range []string{"slow-app", "slow-proxied"} {
			_, err := utils.Run(exec.Command("kubectl", "rollout", "status", "deployment/"+deployment,
				"-n", ns, "--timeout=3m"))
			Expect(err).NotTo(HaveOccurred(), DeploymentDiagnostics(ns, deployment))
		}
		startCurlPod()
	})

	AfterAll(func() {
		for _, id := range userIDs {
			_, _ = kcadm("delete", "users/"+id, "-r", kcRealm)
		}
		CleanupTestNamespace(ns)
	})

	It("applies each route's timeouts at the gateway, and drops them when they are removed", func() {
		const name = "timeouts-routes"
		const host = "timeouts-routes.nebari.local"
		applyYAML(fmt.Sprintf(`
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: %s
  namespace: %s
spec:
  hostname: %s
  service:
    name: slow-app
    port: 8080
  routing:
    routes:
      - pathPrefix: /plain
      - pathPrefix: /long
        timeouts: {request: %s}
      - pathPrefix: /short
        timeouts: {request: 5s}
      - pathPrefix: /backend
        timeouts: {request: %s, backendRequest: 3s}
      - pathPrefix: /off
        timeouts: {request: 0s}
`, name, ns, host, routeTimeout, routeTimeout))
		waitForCondition(name, "Ready")
		waitForRoute(name+"-route", host, "/plain")

		By("timing requests that outlast the default and the routes' timeouts")
		r := sendTimed("-",
			timedRequest{"plain-slow", host, "/plain?s=20"},
			timedRequest{"plain-stream", host, "/plain?stream=20"},
			timedRequest{"long-slow", host, "/long?s=20"},
			timedRequest{"long-stream", host, "/long?stream=20"},
			timedRequest{"long-cap", host, "/long?s=50"},
			timedRequest{"short", host, "/short?s=10"},
			timedRequest{"backend-slow", host, "/backend?s=10"},
			timedRequest{"backend-fast", host, "/backend?s=1"},
			timedRequest{"off", host, "/off?s=50"},
		)
		expectTimedOut(r["plain-slow"], gatewayDefaultTimeout, "untimed route keeps the default")
		// A stream's status and headers go out at once, so the default cuts
		// the body short and the client still sees a 200.
		Expect(r["plain-stream"].code).To(Equal("200"))
		Expect(r["plain-stream"].lines).To(BeNumerically("<", 20), "untimed stream is cut at the default")
		Expect(r["long-slow"].code).To(Equal("200"), "timed route outlasts the default")
		Expect(r["long-stream"].code).To(Equal("200"))
		Expect(r["long-stream"].lines).To(Equal(20), "timed stream arrives whole")
		expectTimedOut(r["long-cap"], routeTimeout, "the request timeout still applies")
		expectTimedOut(r["short"], 5*time.Second, "a timeout shorter than the default")
		expectTimedOut(r["backend-slow"], 3*time.Second, "backendRequest bounds the backend")
		Expect(r["backend-fast"].code).To(Equal("200"))
		Expect(r["off"].code).To(Equal("200"), "0s disables the timeout")

		By("removing every timeout from the spec")
		_, err := utils.Run(exec.Command("kubectl", "patch", "nebariapp", name, "-n", ns, "--type=merge", "-p",
			`{"spec":{"routing":{"routes":[{"pathPrefix":"/plain"},{"pathPrefix":"/long"}]}}}`))
		Expect(err).NotTo(HaveOccurred())
		Eventually(func() []string { return routeRules(name + "-route") }, 2*time.Minute, 5*time.Second).
			Should(Equal([]string{"PathPrefix /plain PathPrefix /long timeouts="}))
		Eventually(func(g Gomega) {
			r := sendTimed("-", timedRequest{"long-default", host, "/long?s=20"})
			g.Expect(r["long-default"].code).To(Equal("504"), "the route is back on the default")
		}, 3*time.Minute, 5*time.Second).Should(Succeed())
	})

	It("times a whole hostname next to routes the app does not own, through a proxy sidecar", func() {
		const name = "timeouts-host"
		const host = "timeouts-host.nebari.local"
		By("timing the whole hostname: the only route is /, so there is no untimed rule")
		applyYAML(fmt.Sprintf(`
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: %s
  namespace: %s
spec:
  hostname: %s
  service:
    name: slow-proxied
    port: 4180
  routing:
    routes:
      - pathPrefix: /
        timeouts: {request: %s}
`, name, ns, host, routeTimeout))
		waitForCondition(name, "Ready")
		Eventually(func() []string { return routeRules(name + "-route") }, time.Minute, 5*time.Second).
			Should(Equal([]string{fmt.Sprintf(`PathPrefix / timeouts={"request":"%s"}`, routeTimeout)}))

		By("adding routes the app's chart owns on the same hostname and listener")
		// The generated route attaches to the app's own HTTPS listener when the
		// operator manages its certificate, or to the shared one otherwise. A
		// chart's routes have to use the same listener, so copy it.
		section, err := utils.Run(exec.Command("kubectl", "get", "httproute", name+"-route", "-n", ns,
			"-o", "jsonpath={.spec.parentRefs[0].sectionName}"))
		Expect(err).NotTo(HaveOccurred())
		Expect(section).NotTo(BeEmpty())
		applyYAML(fmt.Sprintf(`
apiVersion: gateway.envoyproxy.io/v1alpha1
kind: HTTPRouteFilter
metadata:
  name: deny-internal
  namespace: %[1]s
spec:
  directResponse:
    statusCode: 403
    contentType: text/plain
    body:
      type: Inline
      inline: "forbidden\n"
---
apiVersion: gateway.networking.k8s.io/v1
kind: HTTPRoute
metadata:
  name: %[2]s-chart
  namespace: %[1]s
spec:
  parentRefs:
    - name: nebari-gateway
      namespace: envoy-gateway-system
      sectionName: %[4]s
  hostnames: [%[3]s]
  rules:
    - matches: [{path: {type: PathPrefix, value: /api/internal}}]
      filters:
        - type: ExtensionRef
          extensionRef: {group: gateway.envoyproxy.io, kind: HTTPRouteFilter, name: deny-internal}
    - matches: [{path: {type: Exact, value: /}}]
      filters:
        - type: RequestRedirect
          requestRedirect: {path: {type: ReplaceFullPath, replaceFullPath: /app/}, statusCode: 302}
    - matches: [{path: {type: Exact, value: /favicon.ico}}]
      filters:
        - type: RequestRedirect
          requestRedirect: {path: {type: ReplaceFullPath, replaceFullPath: /static/favicon.ico}, statusCode: 301}
`, ns, name, host, section))
		waitForRoute(name+"-chart", host, "/app/")
		waitForRoute(name+"-route", host, "/app/")

		r := sendTimed("-",
			timedRequest{"deny", host, "/api/internal"},
			timedRequest{"deny-sub", host, "/api/internal/x?s=20"},
			timedRequest{"root", host, "/"},
			timedRequest{"favicon", host, "/favicon.ico"},
			timedRequest{"slow", host, "/app/?s=35"},
			timedRequest{"stream", host, "/x?stream=20"},
			timedRequest{"cap", host, "/x?s=50"},
		)
		By("the longer and exact matches of the chart's routes win over /")
		Expect(r["deny"].code).To(Equal("403"))
		Expect(r["deny-sub"].code).To(Equal("403"))
		Expect(r["deny-sub"].seconds).To(BeNumerically("<", 5), "the deny answers without reaching the app")
		Expect(r["root"].code).To(Equal("302"))
		Expect(r["root"].location).To(Equal("https://" + host + "/app/"))
		Expect(r["favicon"].code).To(Equal("301"))
		Expect(r["favicon"].location).To(Equal("https://" + host + "/static/favicon.ico"))
		By("everything else gets the hostname's timeout, through the sidecar")
		// 35s is past both the gateway default and the sidecar's own default.
		Expect(r["slow"].code).To(Equal("200"))
		Expect(r["stream"].code).To(Equal("200"))
		Expect(r["stream"].lines).To(Equal(20))
		expectTimedOut(r["cap"], routeTimeout, "the hostname's timeout still applies")
	})

	It("times protected routes behind auth while public routes keep their own timeouts", func() {
		if _, err := utils.Run(exec.Command("kubectl", "get", "namespace", kcNamespace)); err != nil {
			Skip("Keycloak not installed")
		}
		const name = "timeouts-auth"
		const host = "timeouts-auth.nebari.local"
		const username = "e2e-timeouts"

		By("creating a Keycloak user")
		_, err := kcadm("config", "credentials", "--server", "http://localhost:8080/auth",
			"--realm", "master", "--user", "admin", "--password", "admin")
		Expect(err).NotTo(HaveOccurred())
		userIDs = append(userIDs, createUser(username))

		applyYAML(fmt.Sprintf(`
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: %s
  namespace: %s
spec:
  hostname: %s
  service:
    name: slow-app
    port: 8080
  routing:
    routes:
      - pathPrefix: /
        timeouts: {request: %s}
    publicRoutes:
      - pathPrefix: /static/
        pathType: PathPrefix
      - pathPrefix: /assets/
        pathType: PathPrefix
        timeouts: {request: %s}
      - pathPrefix: /signed-out
  auth:
    enabled: true
    provider: keycloak
`, name, ns, host, routeTimeout, routeTimeout))
		waitForCondition(name, "AuthReady")
		waitForCondition(name, "Ready")
		waitForRoute(name+"-public-route", host, "/signed-out")

		By("anonymous requests: protected paths redirect, public paths answer")
		r := sendTimed("-",
			timedRequest{"protected", host, "/x"},
			timedRequest{"static", host, "/static/a?s=20"},
			timedRequest{"assets", host, "/assets/a?s=20"},
			timedRequest{"signed-out", host, "/signed-out?s=1"},
		)
		Expect(r["protected"].code).To(Equal("302"))
		Expect(r["protected"].location).To(ContainSubstring("/protocol/openid-connect/auth"))
		expectTimedOut(r["static"], gatewayDefaultTimeout, "timing / does not time the public routes")
		Expect(r["assets"].code).To(Equal("200"), "a timed public route")
		Expect(r["signed-out"].code).To(Equal("200"))

		By("logging in, then timing protected requests with the session")
		script, err := utils.LoadTestDataFile("oidc-login.sh", nil)
		Expect(err).NotTo(HaveOccurred())
		login := exec.Command("kubectl", "exec", "-i", "-n", ns, "curl", "--",
			"sh", "-s", "--", host, gatewayIP, username, e2ePassword)
		login.Stdin = strings.NewReader(script)
		out, err := utils.Run(login)
		Expect(err).NotTo(HaveOccurred(), out)
		Expect(parseResult(out)).To(Equal("200"), "login")
		// oidc-login.sh leaves the session in this cookie jar.
		r = sendTimed("/tmp/cookies",
			timedRequest{"authed-slow", host, "/x?s=20"},
			timedRequest{"authed-stream", host, "/x?stream=20"},
			timedRequest{"authed-cap", host, "/x?s=50"},
		)
		Expect(r["authed-slow"].code).To(Equal("200"))
		Expect(r["authed-stream"].code).To(Equal("200"))
		Expect(r["authed-stream"].lines).To(Equal(20))
		expectTimedOut(r["authed-cap"], routeTimeout, "the protected route's timeout")
	})
})
