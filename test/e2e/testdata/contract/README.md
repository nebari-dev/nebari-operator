# Contract matrix

`contract_test.go` checks the operator the way packs and their users meet it.
Each directory here is a sample app shaped like a real pack. Personas sign in
to each app through the real browser flow, and the test compares what they get
with the app's `expect.yaml`.

When a change alters what a persona gets, the test fails. If the change is
intended, update `expect.yaml` in the same PR. The diff then shows reviewers
what changes for packs, the way the Helm chart goldens do.

## Personas

Seeded in the `nebari` realm by the test, with the e2e password:

| Persona | User | Groups |
| ------- | ---- | ------ |
| `anonymous` | none | none |
| `no-group` | `ct-no-group` | none |
| `team-a` | `ct-team-a` | `/ct-team-a` |
| `team-a-sub` | `ct-team-a-sub` | `/ct-team-a/sub` |
| `team-b` | `ct-team-b` | `/ct-team-b` |

## Sample apps

| App | Shape |
| --- | ----- |
| `gateway-signin` | The gateway runs sign-in and forwards the access token; `/healthz` is public |
| `group-gated` | The gateway admits members of `ct-team-a` only, listed by bare name; `/healthz` is public |
| `group-paths` | The gateway admits `/ct-team-b` and `/ct-team-a/sub`, listed as paths; `/healthz` is public |
| `app-login` | The app runs its own sign-in with the operator's client Secret (JupyterHub's shape); `auth.groups` is set but left to the app, so no SecurityPolicy |

Each app has a `nebariapp.yaml`, an `expect.yaml`, and, when it needs more
than `claims-echo.yaml`, a `workload.yaml`. `claims-echo` answers every request
with the claims of the access token it received.

## `expect.yaml`

```yaml
paths:
  /:                  # every path the probe requests
    anonymous: login  # one entry per persona, all of them
    team-a: 200
claims:               # optional
  path: /             # the path whose response carries the claims
  personas:
    team-a:
      azp: e2e-contract-gateway-signin
      groups: [/ct-team-a]
    no-group:
      groups: null    # null: the claim must be absent
```

An outcome is `login` when the response redirects to the Keycloak sign-in
page, otherwise the HTTP status (`200`, `403`), or `<status>:<location>` for
any other redirect. Signed-in personas sign in at the first path whose
anonymous outcome is `login`, then request every path with that session.

Every persona must be listed for every path, so adding a persona means stating
what it gets everywhere. Under `claims`, only the listed claims are compared.

## Adding a sample app

1. Copy the `NebariApp` from the pack it represents, trimmed to the fields
   that matter, into `<app>/nebariapp.yaml` with `namespace:
   NAMESPACE_PLACEHOLDER` and a `*.contract.nebari.local` hostname.
2. Write `<app>/expect.yaml`.
3. Add the app to `contractApps` in `contract_test.go`.
