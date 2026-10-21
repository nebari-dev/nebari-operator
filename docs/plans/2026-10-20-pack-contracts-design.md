# Pack Contracts Design

**Issue:** [#210](https://github.com/nebari-dev/nebari-operator/issues/210)
**Date:** 2026-10-20
**Status:** Draft for discussion
**Related:** [nebari-infrastructure-core#670](https://github.com/nebari-dev/nebari-infrastructure-core/issues/670) (question 1: how packs wire to each other), [nebari-infrastructure-core#641](https://github.com/nebari-dev/nebari-infrastructure-core/pull/641) (Crossplane spin PoC), [nebari-infrastructure-core#549](https://github.com/nebari-dev/nebari-infrastructure-core/pull/549) (spins design doc), [`docs/pack-specification.md`](../pack-specification.md)

## Problem

Software packs need to use each other's outputs. dask-gateway needs JupyterHub's API token and hub URL. A dashboard needs a database's service name and port. A pack that calls another pack's API needs that pack's OIDC client ID.

Today every one of those values is hardcoded in the consumer's values, in a spin, or in a composition template. The Crossplane PoC in #641 showed what that costs:

- **Nobody checks the wiring.** The composition knew z2jh's hub Secret key path, data-science-pack's OIDC Secret naming, the operator's client-id convention, dask-gateway's traefik service name, JupyterHub's pod labels, and ports 8786 and 8081. It guessed the client-id convention and got it wrong. Nothing failed at build or test time.
- **Secrets leave Secrets.** dask-gateway-pack reads the hub token from a Secret in its own namespace. Installed in a separate namespace, the token has nowhere to come from, so the PoC copied it into the ArgoCD Application's `valuesObject`. That puts the token in plaintext in etcd, in `kubectl get application -o yaml`, and in the ArgoCD UI.
- **Desired state depended on observed state.** The composition gated the whole dask-gateway Application on having read the token. Crossplane garbage-collects what a pipeline stops returning, and the Application had `prune: true` and the ArgoCD finalizer, so one empty Secret read would uninstall dask-gateway.

The team decided on 2026-10-15 that a spin is a one-time bootstrap render into the GitOps repo, not a reconciled composition, and dropped the Crossplane approach. That makes the wiring problem sharper, not smaller: after bootstrap nothing is in the loop, so any value that changes on day 2 (a rotated token, a renamed client, a new hostname) has to reach consumers through something that runs in the cluster.

## Goals

- A pack declares what it **provides** (outputs) and what it **requires** from other packs (inputs, required or optional), the way an OpenTofu module declares outputs and variables.
- Those declarations are **discoverable in the cluster**: `kubectl get contracts -A` lists them, and a contract's status shows its resolved plain outputs.
- A consumer in another namespace can use a producer's **secret** output through an ordinary same-namespace `secretKeyRef`, with no plaintext copy anywhere else.
- **The producer decides who may read** a restricted output. The consumer has to ask. Nothing is copied without both.
- Wiring stays **explicit**: a consumer names the producer contract it binds to. Nothing is matched automatically.
- **Failure is conservative.** A producer that is temporarily unreadable never causes a consumer to lose what it already has, and nothing is ever uninstalled because a read failed.
- Values the operator already knows (hostname, OIDC client ID, gateway) are **published by the operator**, so no pack re-derives a convention.

## Non-goals

- **Render-time inputs.** Values Helm needs to decide what to template (storage class, GPU node group, domain) stay in chart values and in NIC's `nebari-integration.yaml`. The contract describes runtime wiring only. Duplicating render-time inputs into a CRD would give them two homes that drift.
- **Composition or ordering of installs.** Spins and ArgoCD own what gets installed and when. Contracts only move values between packs that are already installed.
- **Generating NetworkPolicies or RBAC from contracts.** #670 lists the network and identity surface (pod labels, service accounts) as contract material. This design leaves room for those output kinds but does not build them. See Follow-ups.
- **Making contracts a pack requirement.** Contracts are optional in v1alpha1. Adding them to the pack specification as a MUST is a platform contract change that needs a platform RFD under [pack-specification section 8](../pack-specification.md#8-changing-this-specification).

## Anti-goals

These would make the design a failure even if it worked:

- A consumer's Secret, Application, or pods are deleted because a producer was briefly unreadable (the #641 failure).
- A secret value appears anywhere other than a Kubernetes Secret.
- Someone who can create a contract in namespace A can read a Secret from namespace B that B never offered to A.
- A pack author has to learn a composition engine to publish or consume an output.

## Design

### `PackContract` CRD

The operator installs one CRD, `PackContract` (`reconcilers.nebari.dev/v1alpha1`, short name `contract`). Pack authors write instances of it in their charts, next to their NebariApp.

A pack can publish any number of contracts, one per role it plays. data-science-pack might publish one for JupyterHub (hub URL, API token) and another for shared storage. Consumers bind to a single contract by `{namespace, name}`.

Every contract references at least one NebariApp **in its own namespace**. That reference does two jobs:

1. It identifies the contract as belonging to a pack. A software pack is not a pack without a NebariApp, so a contract with no live NebariApp is not Ready.
2. It is the source for outputs the operator already knows, so the author never restates them.

Every officially supported pack at packs.nebari.dev has a routable component (confirmed by the maintainers), so requiring a NebariApp costs nothing today. A future pack with no routable component would need NebariApp's `hostname` and `service` to become optional first. That is a separate change, listed under Follow-ups.

`NebariApp` stays at `v1`. The new kind starts at `v1alpha1` in the same group so its shape can change while the team learns from real packs.

### Outputs

Each entry in `spec.provides` has a name, a description, and exactly one source:

| Source | Example | Visibility | Resolved value |
|---|---|---|---|
| `value` | `value: "8786"` | plain | the literal |
| `fromApp` | `fromApp: {name: jupyterhub, field: url}` | plain | computed from the NebariApp with the operator's own naming functions |
| `service` | `service: {name: proxy-public, port: 80}` | plain | `proxy-public.data-science.svc.cluster.local:80`, plus structured name, namespace and port in status |
| `configMap` | `configMap: {name: hub-config, key: base_url}` | plain | the key's value |
| `secret` | `secret: {name: hub, key: hub.services.dask-gateway.apiToken}` | restricted | value never published; status shows the source name and key |

`fromApp.field` is one of `hostname`, `url` (`https://<hostname>`, or `http://` when the NebariApp sets `routing.tls.enabled: false`), `clientId` (`naming.ClientID`, the `<namespace>-<name>` convention), or `gateway` (`naming.GatewayName`). All of them are plain.

The OIDC client Secret is deliberately not an output. The auth reconciler limits it to the app's own ServiceAccount (`internal/controller/reconcilers/auth/rbac.go:33-80`), and projecting it to another namespace would bypass that. A consumer that needs to call a producer as an authenticated client uses token exchange (`spec.auth.tokenExchange`).

The operator computes these with the same functions in `internal/controller/utils/naming` that it uses to create the resources, so an output cannot drift from what the operator actually provisioned. It does not read them from NebariApp status: `status.gatewayRef` and `status.clientSecretRef` are declared in `api/v1/nebariapp_types.go` but nothing sets them today. Only `status.hostname` is populated (`internal/controller/nebariapp_controller.go:124`).

**Plain** outputs appear in the contract's status, where anyone who can read the contract can see them. They are values that are already visible in the cluster to anyone who can list Services and NebariApps.

**Restricted** outputs never have their value in status. Status records that they exist, whether they resolved, and the name and key of the source Secret in the producer's namespace. They reach a consumer in another namespace only by projection, and only if the producer granted that consumer's namespace in `allowedConsumers`. A consumer in the producer's own namespace reads the Secret directly (see Same namespace below).

### Grants

`allowedConsumers` is set per restricted output, not per contract. Each entry is either a namespace name or a namespace label selector:

```yaml
allowedConsumers:
  - namespace: dask-gateway                     # production: name each consumer
  - namespaceSelector: {matchLabels: {team: ds}} # everything one team installs
  - namespaceSelector: {}                        # wildcard, for development
```

Pack authors cannot know at chart-writing time which namespace a consumer will land in, so grants come from chart values that whoever installs the producer sets. At bootstrap, a spin's glue config sets them.

Three rules apply to every grant:

- **Opted-in namespaces only.** A selector only ever matches namespaces labeled `nebari.dev/managed=true`. The producer's own namespace must carry the label too. Labeling a namespace normally takes cluster-level rights, so both ends of a projection have been opted in by someone with those rights.
- **The consumer must ask.** A grant alone copies nothing. The consumer's contract has to list the output under `requires`.
- **Wildcards are off unless the operator allows them.** A wildcard is an empty selector, `namespaceSelector: {}` (decided 2026-10-20: the check is on the syntax, not on what a selector happens to match). A non-empty selector such as `matchLabels: {team: ds}` is an ordinary grant. The operator setting `CONTRACT_ALLOW_WILDCARD_GRANTS` defaults to `false`. When it is false, wildcard entries are ignored and the producer reports `GrantsReady=False / WildcardGrantsDisabled`. When it is true, a contract with a wildcard grant emits a Warning event and sets `status.wildcardGrant: true` so it stands out in `kubectl get`. The dev Kind setup turns it on.

### Requirements and projection

A consumer lists what it needs in `spec.requires`:

```yaml
requires:
  - name: hub
    from: {namespace: data-science, name: jupyterhub}
    version: ">=1.0.0 <2.0.0"
    optional: true
    outputs:
      - output: hubApiToken
        into: {secret: {name: dask-gateway-hub-token, key: token}}
      - output: hubUrl
        into: {configMap: {name: dask-gateway-hub, key: url}}
```

For each projected output, the operator writes the value into a Secret (restricted outputs) or a ConfigMap (plain outputs) **in the consumer's namespace**, under the name and key the consumer chose. The consumer's chart knows that name at render time, so it uses an ordinary same-namespace `secretKeyRef` or `configMapKeyRef`. Kubernetes has no cross-namespace Secret reference for pods (`SecretKeySelector` embeds a `LocalObjectReference`, [k8s.io/api v0.34.1 core/v1/types.go:2567-2575](https://github.com/kubernetes/api/blob/v0.34.1/core/v1/types.go#L2567-L2575)), so a same-namespace copy is the only way an unmodified chart can read the value and keep it in a Secret.

The copy is a derived artifact, the same way the HTTPRoute is derived from a NebariApp. Nobody authors it. The operator:

- **keeps it in sync.** It watches the source and rewrites the copy within one reconcile of a change. A hand edit to the copy is overwritten.
- **owns it.** The copy has an owner reference to the consumer's contract and the label `app.kubernetes.io/managed-by: nebari-operator`. The annotation `nebari.dev/contract-source` lists the `<namespace>.<contract>.<output>` source of each key it holds. It is an annotation rather than a label because one object can hold keys from several sources, and a label value is limited to 63 characters, which a single source can exceed. Deleting the consumer contract deletes the copy.
- **stamps a hash.** The annotation `nebari.dev/contract-value-hash` changes when the value changes, so tools like Reloader can restart pods. Env vars do not pick up a changed Secret until the pod restarts. That is true of any Secret, so the hash is the hook, and restarting is left to the cluster's existing tooling.

**Several keys in one object (builder's call, recorded here).** Many charts want a token and a URL in the same Secret. Projections from one consumer contract may target the same Secret or ConfigMap with different keys. The operator owns the whole object, merges the keys it manages, prunes a key when its projection goes away, and deletes the object when no keys remain. The hash annotation covers the whole object, and the `nebari.dev/contract-source` annotation lists the source of every key. Two different consumer contracts may not share a target object. The second one reports `TargetConflict`.

The single source of truth is the producer's declaration. The copy duplicates a value, not knowledge: the Secret name and key path live in one place, the producer's contract, and the consumer only names the output it wants.

**Same namespace (decided 2026-10-20: no copy).** When producer and consumer share a namespace (dask-gateway-pack's documented default), the operator writes no copy. The consumer reads the producer's Secret directly. No grant is needed, because the consumer could already mount any Secret in its own namespace.

The consumer still declares the requirement, without `into`, so the operator checks that the producer contract and output exist and that the version is in range, and reports the requirement state. The consumer's chart needs the producer's Secret name and key at render time, and a chart cannot read status. So for a same-namespace consumer, **the source Secret name and key of each secret output are part of the producer's versioned interface**. Renaming either is a major-version change, and producers document them next to the output. The producer's `status.outputs[].secretRef` shows them for humans and tooling. Publishing that reference is safe: it names a Secret in the producer's namespace, which pods in any other namespace cannot mount.

`into` is optional in the CRD, because CRD validation rules cannot see `metadata.namespace`. The controller enforces it. A cross-namespace projection without `into` reports `TargetMissing`, and a same-namespace projection with `into` is ignored, with a Warning event.

### Versioning

`spec.version` on a producer is the semver of the contract's **interface**: the set of outputs, their names, and their meaning. It is not the chart version. A producer bumps the major version when it removes or renames an output or changes what one means.

A consumer pins a range in `requires[].version`, parsed with `blang/semver/v4` (`ParseRange`, [v4.0.0 range.go:112](https://github.com/blang/semver/blob/v4.0.0/range.go#L112)), which the module graph already includes. Ranges use full versions: `">=1.0.0 <2.0.0"`.

When the producer's version leaves the range, the operator stops updating the copy, keeps the last value, and reports `VersionMismatch` on that requirement.

### Lifecycle and failure modes

Losing access is deliberate and immediate. Failing to read is temporary and conservative.

| Event | Copy | Consumer requirement state |
|---|---|---|
| Producer grants, consumer asks, source readable | written and kept in sync | `Satisfied` |
| Consumer asks, producer has not granted | not written | `NotGranted` |
| Grant removed from the producer | **deleted** | `NotGranted` |
| Requirement removed from the consumer | **deleted** | (requirement gone) |
| Consumer contract deleted | deleted (owner reference) | (contract gone) |
| Producer contract deleted | kept, last value | `Stale` |
| Producer's source Secret missing or unreadable | kept, last value | `Stale` |
| Producer version leaves the pinned range | kept, last value | `VersionMismatch` |
| Producer namespace or consumer namespace loses `nebari.dev/managed=true` | **deleted** | `NotGranted` |

Revocation deletes because a grant that cannot be withdrawn is not a grant. An outage keeps the copy because deleting it would break running consumers on their next restart over a problem that is probably transient. That is the #641 failure.

The cost is that uninstalling a producer leaves its last values in the consumer's namespace until the consumer drops the requirement. Copies do not expire in v1alpha1 (decided 2026-10-20). The `Stale` state stays visible in `kubectl get` so a leftover copy gets noticed.

### Conditions

Conditions follow the NebariApp pattern: a summary `Ready` plus one condition per concern, with reasons as constants in the API package.

| Condition | On | False when |
|---|---|---|
| `AppsReady` | every contract | a referenced NebariApp is missing (`AppNotFound`) |
| `OutputsResolved` | producers | a source cannot be read (`SourceNotFound`, `AppFieldUnavailable`) |
| `GrantsReady` | producers with restricted outputs | a wildcard grant was ignored (`WildcardGrantsDisabled`) |
| `RequirementsSatisfied` | consumers | a required (non-optional) requirement is not `Satisfied` (`NotGranted`, `Stale`, `VersionMismatch`, `ContractNotFound`) |
| `Ready` | every contract | any of the above is False |

An optional requirement that is not satisfied shows its state in `status.requirements[]` but does not make `RequirementsSatisfied` False.

### How this fits with spins

A spin renders, once, the values that wire its packs together: `allowedConsumers` on each producer and `requires` on each consumer. After bootstrap that is ordinary YAML in the GitOps repo, owned by the user, as the 2026-10-15 meeting decided. Anything a spin does with contracts can be done by hand by editing the same values.

Spins no longer need to know any pack's internals. They name contracts and outputs, which the producer's contract defines and the operator checks.

## Worked example: dask-gateway and data-science-pack

data-science-pack in namespace `data-science`, dask-gateway-pack in namespace `dask-gateway`, both namespaces labeled `nebari.dev/managed=true`.

**Producer.** data-science-pack's chart renders:

```yaml
apiVersion: reconcilers.nebari.dev/v1alpha1
kind: PackContract
metadata:
  name: jupyterhub
  namespace: data-science
spec:
  version: 1.0.0
  apps:
    - name: jupyterhub
  provides:
    - name: hubUrl
      description: Public URL of JupyterHub
      fromApp: {name: jupyterhub, field: url}
    - name: hubService
      description: In-cluster JupyterHub proxy
      service: {name: proxy-public, port: 80}
    - name: hubApiToken
      description: API token for the dask-gateway JupyterHub service
      secret: {name: hub, key: hub.services.dask-gateway.apiToken}
      allowedConsumers:
        - namespace: dask-gateway   # from values: contract.hubApiToken.allowedConsumers
```

After reconcile:

```yaml
status:
  conditions:
    - {type: Ready, status: "True"}
  outputs:
    - {name: hubUrl, value: "https://jupyter.example.com"}
    - name: hubService
      value: "proxy-public.data-science.svc.cluster.local:80"
      service: {namespace: data-science, name: proxy-public, port: 80}
    - name: hubApiToken
      restricted: true
      resolved: true
      secretRef: {name: hub, key: hub.services.dask-gateway.apiToken}
```

**Consumer.** dask-gateway-pack's chart renders:

```yaml
apiVersion: reconcilers.nebari.dev/v1alpha1
kind: PackContract
metadata:
  name: dask-gateway
  namespace: dask-gateway
spec:
  version: 0.3.0
  apps:
    - name: dask-gateway
  requires:
    - name: hub
      from: {namespace: data-science, name: jupyterhub}
      version: ">=1.0.0 <2.0.0"
      optional: true
      outputs:
        - output: hubApiToken
          into: {secret: {name: dask-gateway-hub-token, key: token}}
        - output: hubUrl
          into: {configMap: {name: dask-gateway-hub, key: url}}
```

and points its upstream values at the projected names, which it knows at render time:

```yaml
gateway:
  auth:
    jupyterhub:
      apiTokenFromSecretName: dask-gateway-hub-token
      apiTokenFromSecretKey: token
```

**Sequence.**

1. ArgoCD syncs both Applications in any order. Neither Application ever contains the token.
2. Until the producer's contract exists, the consumer's `hub` requirement is `ContractNotFound`. It is optional, so dask-gateway's contract stays Ready. The dask-gateway pod waits on the missing Secret, as it would for any Secret that does not exist yet.
3. The producer's contract resolves. The operator sees the consumer's request and the producer's grant, writes `dask-gateway-hub-token` and `dask-gateway-hub` into `dask-gateway`, and sets the requirement to `Satisfied`.
4. JupyterHub rotates the token. The operator rewrites the copy and changes the hash annotation.
5. Someone deletes data-science-pack's contract by mistake. The copy stays and the requirement goes `Stale`. dask-gateway keeps working.
6. The installer removes `dask-gateway` from `allowedConsumers`. The copy is deleted and the requirement goes `NotGranted`.

## Pack author workflows

### Producer

1. Add a `PackContract` next to the NebariApp, using the `nebari-app.packContract` helper from the `nebari-app` library chart (see Program layout).
2. List outputs. Use `fromApp` for anything the operator already knows instead of restating it.
3. For each restricted output, expose `allowedConsumers` as a chart value with an empty default. A fresh install grants nothing.
4. Pick `spec.version` starting at `1.0.0`. Bump the major version when you remove, rename, or change the meaning of an output.

### Consumer

1. Add a `PackContract` with a `requires` entry naming the producer contract and a version range.
2. Choose the projected Secret and ConfigMap names, and point the app's values at them.
3. Mark the requirement `optional: true` if the app works without it.
4. Document the producer contract and outputs the chart expects, so installers know what to grant.

### Spin author

1. Pick the packs.
2. In the spin's glue values, set each producer's `allowedConsumers` to the consumer namespaces the spin creates, and enable each consumer's `requires`.
3. Nothing else. The spin never reads or copies a value.

## Authorization model

| Actor | Can | Cannot |
|---|---|---|
| Producer namespace editor | publish outputs, grant them to namespaces | read anything from another namespace |
| Consumer namespace editor | request a granted output into its own namespace | receive an output that was not granted, or write into another namespace |
| Cluster admin | label namespaces `nebari.dev/managed=true`, enable wildcard grants | (no new rights) |
| Operator | read sources in opted-in producer namespaces, write copies in opted-in consumer namespaces | read a source for an output that no contract declares |

The operator already has cluster-wide `get/list/watch/create/update/patch` on Secrets (`internal/controller/nebariapp_controller.go:65`). This design adds `delete` on Secrets, because revocation has to remove a copy and owner-reference garbage collection only covers deleting the consumer contract. It also adds full access to ConfigMaps. Both were accepted on 2026-10-20: the operator can already overwrite any Secret, so `delete` adds little real power. In exchange, it narrows what the operator **does** with Secrets: it reads a Secret only when a contract in that Secret's own namespace declares it as an output, and it checks the grant **before** reading. A consumer can therefore never use the operator to learn whether an undeclared Secret exists, unlike `ValidateService` today, which reports whether any Service in any namespace exists and which ports it has (`internal/controller/reconcilers/core/reconciler.go:118-155`).

## Alternatives considered

**kro (`ResourceGraphDefinition`).** An RGD looks like a module: `schema.spec` for inputs with defaults, `schema.status` for outputs, CEL type checking, and `externalRef` to read another resource. It would give discoverability through generated CRDs. It was rejected because every pack would need its own RGD, which is more for a pack author to write and maintain than an instance of one shared CRD. Pack authors would also have to learn kro, the same objection that ended the Crossplane approach. kro's default RBAC mode is unrestricted, its API is `v1alpha1`, and I could not find in its docs what happens to already-created children when an `externalRef` disappears, which is the #641 failure. kro remains a candidate for the separate "reusable composition you can deploy anywhere" concept from the 2026-10-15 meeting.

**Crossplane compositions (#641).** Dropped by the team on 2026-10-15. It requires pack authors to learn Crossplane, its garbage collection fights users who add packs by hand, and desired state that depends on observed state uninstalls things when a read fails.

**Extend NebariApp with a `contract` block.** It avoids a second object, but NebariApp already carries Keycloak group and protocol-mapper authority. Grants would ride on the same RBAC verb as realm configuration. NebariApp is a routable service, one per role, while a contract may span roles. NebariApp's version is cluster-wide and slow-moving, while contracts need to evolve independently. A separate kind keeps each object to one job.

**RBAC-only grants (no copy).** The producer's grant could create a Role allowing the consumer's service account to `get` the one Secret. Nothing is copied, but the consumer then has to fetch the Secret through the Kubernetes API, which no upstream chart we wrap does. It stays a possible future output mode for API-aware consumers.

**Inline values in the Application (#641's approach).** Puts secrets in `valuesObject` in plaintext, and makes the Application change when the value changes, which couples secret rotation to ArgoCD syncs.

**Install producer and consumer in one namespace.** Works today with no copy, and stays supported. It does not cover packs that need their own namespace for isolation, quotas, or ownership.

**Automatic binding by capability ("give me any OIDC provider").** Rejected in favor of explicit binding. Matching by capability creates ambiguity when two producers exist, and wiring changes when someone installs an unrelated pack. Discovery here means you can list what is installed and what it exposes, not that something gets bound for you.

## Program Design

**Scale:** full

### Existing patterns

| Pattern | Tag | Source | Promote? |
|---|---|---|---|
| Condition types, reasons and event reasons are Go constants in the API package, never re-typed strings | [documented: AGENTS.md:248] | `api/v1/nebariapp_types.go` | n/a |
| `docs/reconcilers/` is the source of truth for reconciler behavior and is updated in the same PR | [documented: AGENTS.md:270, :277] | `docs/reconcilers/*.md` | n/a |
| Sub-reconcilers own one concern each and do not import one another | [documented: AGENTS.md:22, :202, :225] | `internal/controller/reconcilers/{core,routing,tls,auth}` | n/a |
| Table-driven unit tests next to the code, envtest suite in `internal/controller`, e2e on Kind for cross-resource behavior | [documented: AGENTS.md:229, :262-266] | `internal/controller/suite_test.go`, `test/e2e/` | n/a |
| Validation is CEL `XValidation` markers on the types; the operator ships no admission webhooks | [inferred] | `api/v1/nebariapp_types.go:174`; no `config/webhook/` | pending |
| Operator settings are env vars loaded by `internal/config` with `getEnv*` helpers and set in `config/manager/manager.yaml`, from which the Helm chart is generated | [inferred] | `internal/config/tls.go:29`, `internal/config/auth.go:171-205`, `Makefile:176-180` | pending |
| Watched dependents are mapped back to their owner with `handler.EnqueueRequestsFromMapFunc` | [inferred] | `internal/controller/nebariapp_controller.go:413-433` | pending |
| Namespace opt-in via `nebari.dev/managed=true`, checked before any work | [inferred] | `internal/controller/reconcilers/core/reconciler.go:102-118` | pending |
| Pack charts render operator CRs through `nebari-app` library chart defines with `required` checks | [inferred] | `charts/nebari-app/templates/_nebari-app.tpl:15-40` | pending |
| Generated resources carry owner references to their NebariApp | [documented: AGENTS.md:200] | `controllerutil.SetControllerReference` in `routing/httproute.go:217`, `auth/reconciler.go:505` | n/a |

### Component contracts

| Contract | Status | Owner | Consumers | Verified by | Promote? | Notes |
|---|---|---|---|---|---|---|
| `PackContract` CRD (`reconcilers.nebari.dev/v1alpha1`) | new | nebari-operator | pack charts, spins, NIC | n/a (new) | n/a | spec and status shapes below |
| Projected Secret/ConfigMap shape: owner reference, `app.kubernetes.io/managed-by` label, `nebari.dev/contract-source` and `nebari.dev/contract-value-hash` annotations | new | nebari-operator | consumer pods, Reloader-style tooling | n/a (new) | n/a | consumers rely only on name and key |
| `CONTRACT_ALLOW_WILDCARD_GRANTS` operator setting | new | nebari-operator | `config/manager/manager.yaml`, generated Helm chart, `dev/` setup | n/a (new) | n/a | default `false` |
| `nebari-app.packContract` library chart define | new | nebari-operator (`charts/nebari-app`) | pack charts | n/a (new) | n/a | mirrors `nebari-app.nebariApp` |
| `nebari.dev/managed=true` namespace label | changed-compatible | nebari-operator | NebariApp core reconciler, PackContract reconciler | `rg -n 'nebari.dev/managed' internal/ docs/ charts/` | pending | gains a second reader; meaning unchanged |
| Operator naming functions `ClientID`, `ClientSecretName`, `GatewayName` (implicit) | unchanged (implicit) | nebari-operator | Keycloak provider, routing, pack-specification section 3, PackContract `fromApp` | `rg -n '^func (ClientID\|ClientSecretName\|GatewayName)' internal/controller/utils/naming` | pending | published as outputs so packs stop re-deriving them |
| Operator ClusterRole | changed-compatible | nebari-operator | install manifests, generated chart | `rg -n 'kubebuilder:rbac' internal/controller/` | n/a | adds `packcontracts`, full `configmaps`, and `delete` on `secrets` |

**`PackContract` CRD.** Promises: the spec fields and status shapes in Data models; resolved plain outputs in `status.outputs`; restricted outputs never in status; per-requirement state in `status.requirements`. Requires: at least one `apps` entry naming a NebariApp in the same namespace; unique output and requirement names; exactly one source per output and one target per projection (enforced by CEL). Errors: reported as conditions, never as rejected reconciles, except CEL validation at admission.

**Projected resource shape.** Promises: a Secret (restricted) or ConfigMap (plain) holding exactly the requested keys, in the consumer's namespace, owned by the consumer contract, and kept in sync. A revoked key is pruned, and the object is deleted when no keys remain. Requires: the target name is not already used by a resource the operator does not own. If it is, the requirement reports `TargetConflict` and the operator does not overwrite it.

**`CONTRACT_ALLOW_WILDCARD_GRANTS`.** Promises: when unset or false, empty namespace selectors grant nothing. Error behavior: unparseable values are treated as false (matches `getEnvBool`).

### Data models and types

New package `api/v1alpha1` (`groupversion_info.go` with group `reconcilers.nebari.dev`, version `v1alpha1`).

```go
// PackContract publishes a pack's runtime outputs and requests outputs from
// other packs.
// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:shortName=contract
// +kubebuilder:printcolumn:name="Version",type=string,JSONPath=`.spec.version`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Wildcard",type=boolean,JSONPath=`.status.wildcardGrant`
type PackContract struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`
	Spec   PackContractSpec   `json:"spec"`
	Status PackContractStatus `json:"status,omitempty"`
}

type PackContractSpec struct {
	// Version is the semver of this contract's interface, not the chart.
	// +kubebuilder:validation:Pattern=`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$`
	Version string `json:"version"`

	// Apps are NebariApps in this namespace that this contract belongs to.
	// +kubebuilder:validation:MinItems=1
	// +listType=map
	// +listMapKey=name
	Apps []LocalAppReference `json:"apps"`

	// +listType=map
	// +listMapKey=name
	// +optional
	Provides []Output `json:"provides,omitempty"`

	// +listType=map
	// +listMapKey=name
	// +optional
	Requires []Requirement `json:"requires,omitempty"`
}

type LocalAppReference struct {
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`
}

// +kubebuilder:validation:XValidation:rule="[has(self.value), has(self.fromApp), has(self.service), has(self.configMap), has(self.secret)].filter(x, x).size() == 1",message="exactly one output source must be set"
// +kubebuilder:validation:XValidation:rule="!has(self.allowedConsumers) || has(self.secret)",message="allowedConsumers applies only to secret outputs"
type Output struct {
	// +kubebuilder:validation:Pattern=`^[a-z][a-zA-Z0-9]*$`
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`

	Value     *string            `json:"value,omitempty"`
	FromApp   *AppFieldSource    `json:"fromApp,omitempty"`
	Service   *ServiceSource     `json:"service,omitempty"`
	ConfigMap *KeySource         `json:"configMap,omitempty"`
	Secret    *KeySource         `json:"secret,omitempty"`

	// AllowedConsumers grants a restricted output to consumer namespaces.
	// +optional
	AllowedConsumers []ConsumerSelector `json:"allowedConsumers,omitempty"`
}

type AppFieldSource struct {
	Name string `json:"name"`
	// +kubebuilder:validation:Enum=hostname;url;clientId;gateway
	Field string `json:"field"`
}

type ServiceSource struct {
	Name string `json:"name"`
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`
}

type KeySource struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

// +kubebuilder:validation:XValidation:rule="has(self.__namespace__) != has(self.namespaceSelector)",message="set exactly one of namespace or namespaceSelector"
type ConsumerSelector struct {
	Namespace         string                `json:"namespace,omitempty"`
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`
}

type Requirement struct {
	Name string            `json:"name"`
	From ContractReference `json:"from"`
	// Version is a blang/semver range, for example ">=1.0.0 <2.0.0".
	Version  string `json:"version"`
	Optional bool   `json:"optional,omitempty"`
	// +kubebuilder:validation:MinItems=1
	Outputs []Projection `json:"outputs"`
}

type ContractReference struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
}

// +kubebuilder:validation:XValidation:rule="!has(self.into) || has(self.into.secret) != has(self.into.configMap)",message="set exactly one projection target"
type Projection struct {
	Output string `json:"output"`
	// Into is required when the producer is in another namespace and
	// ignored when it is in the same one. The controller enforces this.
	// +optional
	Into *ProjectionTarget `json:"into,omitempty"`
}

type ProjectionTarget struct {
	Secret    *KeyTarget `json:"secret,omitempty"`
	ConfigMap *KeyTarget `json:"configMap,omitempty"`
}

type KeyTarget struct {
	Name string `json:"name"`
	Key  string `json:"key"`
}

type PackContractStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	Outputs            []ResolvedOutput   `json:"outputs,omitempty"`
	Requirements       []RequirementStatus `json:"requirements,omitempty"`
	WildcardGrant      bool               `json:"wildcardGrant,omitempty"`
}

type ResolvedOutput struct {
	Name       string           `json:"name"`
	Value      string           `json:"value,omitempty"`      // plain only
	Service    *ServiceEndpoint `json:"service,omitempty"`
	SecretRef  *KeySource       `json:"secretRef,omitempty"` // restricted: name and key only
	Restricted bool             `json:"restricted,omitempty"`
	Resolved   bool             `json:"resolved"`
	Message    string           `json:"message,omitempty"`
}

type ServiceEndpoint struct {
	Namespace string `json:"namespace"`
	Name      string `json:"name"`
	Port      int32  `json:"port"`
}

type RequirementStatus struct {
	Name            string `json:"name"`
	// +kubebuilder:validation:Enum=Satisfied;NotGranted;Stale;VersionMismatch;ContractNotFound;OutputNotFound;TargetConflict;TargetMissing
	State           string      `json:"state"`
	ProducerVersion string      `json:"producerVersion,omitempty"`
	LastSyncTime    metav1.Time `json:"lastSyncTime,omitempty"`
	Message         string      `json:"message,omitempty"`
}
```

Condition types and reasons (`AppsReady`, `OutputsResolved`, `GrantsReady`, `RequirementsSatisfied`, `Ready`; the state names above; `AppNotFound`, `SourceNotFound`, `AppFieldUnavailable`, `WildcardGrantsDisabled`) are constants in `api/v1alpha1`, per AGENTS.md:248.

The `ConsumerSelector` CEL rule uses `__namespace__` because `namespace` is a CEL reserved word; check the escaping against the Kubernetes CEL docs for the cluster's minimum version when implementing.

### Method signatures

```go
// internal/config/contract.go
type ContractConfig struct{ AllowWildcardGrants bool }
func LoadContractConfig() ContractConfig

// internal/controller/packcontract_controller.go
type PackContractReconciler struct {
	client.Client
	Scheme      *runtime.Scheme
	Recorder    record.EventRecorder
	Resolver    *contracts.OutputResolver
	Projector   *contracts.Projector
	Config      config.ContractConfig
}
func (r *PackContractReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error)
func (r *PackContractReconciler) SetupWithManager(mgr ctrl.Manager) error

// internal/controller/reconcilers/contracts/resolve.go
type OutputResolver struct {
	Reader client.Reader
}
func (r *OutputResolver) ResolvePlain(ctx context.Context, c *v1alpha1.PackContract) ([]v1alpha1.ResolvedOutput, error)
func (r *OutputResolver) ReadRestricted(ctx context.Context, producer *v1alpha1.PackContract, output string) ([]byte, error)

// internal/controller/reconcilers/contracts/grants.go
func IsGranted(out v1alpha1.Output, consumerNS *corev1.Namespace, allowWildcard bool) (granted bool, usedWildcard bool, err error)

// internal/controller/reconcilers/contracts/version.go
func InRange(producerVersion, rng string) (bool, error)

// internal/controller/reconcilers/contracts/project.go
type Projector struct {
	Client client.Client
	Scheme *runtime.Scheme
}
func (p *Projector) Apply(ctx context.Context, consumer *v1alpha1.PackContract, desired []DesiredProjection) error
func (p *Projector) Prune(ctx context.Context, consumer *v1alpha1.PackContract, keep []DesiredProjection) error

type DesiredProjection struct {
	Requirement string
	Output      string
	Target      v1alpha1.KeyTarget
	Kind        ProjectionKind // Secret or ConfigMap
	Value       []byte
	Source      string // "<namespace>.<contract>.<output>" for the contract-source annotation
}
```

`OutputResolver` takes a `client.Reader` so tests can inject a fake client. It calls the `naming` package directly, as the routing and auth reconcilers do.

### Program layout

```text
+ api/v1alpha1/groupversion_info.go          # follows api/v1/groupversion_info.go
+ api/v1alpha1/packcontract_types.go         # follows api/v1/nebariapp_types.go (types + constants)
+ api/v1alpha1/zz_generated.deepcopy.go      # generated
+ config/crd/bases/reconcilers.nebari.dev_packcontracts.yaml   # generated
~ config/crd/kustomization.yaml              # add the new CRD
~ config/rbac/role.yaml                      # generated from markers
~ config/manager/manager.yaml                # CONTRACT_ALLOW_WILDCARD_GRANTS=false
~ config/samples/                            # producer and consumer samples
+ internal/config/contract.go                # follows internal/config/tls.go
+ internal/controller/packcontract_controller.go   # follows nebariapp_controller.go
+ internal/controller/reconcilers/contracts/{resolve,grants,version,project}.go  # one concern per file, like reconcilers/auth
~ cmd/operator/main.go                       # register v1alpha1 scheme, construct and set up the reconciler
~ go.mod                                     # blang/semver/v4 becomes a direct dependency
~ charts/nebari-app/templates/_pack-contract.tpl  # nebari-app.packContract define
~ docs/using-the-nebari-app-chart.md         # contract section
+ docs/reconcilers/contracts.md              # reconciler behavior record
+ test/e2e/packcontract_test.go              # journeys
~ dev/                                       # enable wildcard grants in the Kind setup
```

### Call stacks

```text
Production: PackContract reconcile (request for <ns>/<name>)
  PackContractReconciler.Reconcile
    → Get PackContract; if namespace lacks nebari.dev/managed=true
      → Projector.Prune(keep=none) for copies this contract owns, Ready=False, stop
    → check spec.apps NebariApps exist (client.Get each) → AppsReady
    → OutputResolver.ResolvePlain
      → client.Get NebariApp / Service / ConfigMap per plain output
      → existence check (no value read) for restricted outputs
    → set status.outputs, OutputsResolved, GrantsReady, wildcardGrant
    → for each spec.requires
      → client.Get producer PackContract (ContractNotFound | continue)
      → InRange(producer.spec.version, requirement.version) (VersionMismatch → keep, stop)
      → for each projection
        → find producer output (OutputNotFound)
        → same namespace → no copy, Satisfied, continue
        → no into → TargetMissing, continue
        → IsGranted(output, consumer namespace, Config.AllowWildcardGrants)
        → not granted → mark for deletion, NotGranted
        → granted → OutputResolver.ReadRestricted or plain value
          → read error → keep existing copy, Stale
    → Projector.Apply(desired)   → CreateOrUpdate Secret/ConfigMap with owner ref, label, annotations
    → Projector.Prune(keep)      → delete owned copies no longer desired (revoked, removed)
    → status.Update

Production: watch fan-out
  NebariApp change          → contracts in that namespace listing it in spec.apps
  Secret/ConfigMap change   → producer contracts in that namespace declaring it, then their consumers
  PackContract change       → consumers whose spec.requires.from names it (field index)
  Namespace label change    → contracts in that namespace and consumers of its producers
  Owned Secret/ConfigMap    → owning consumer contract (Owns)
```

### Component trees and DI

```text
cmd/operator/main.go
  config.LoadContractConfig()
  &controller.PackContractReconciler{
      Client:    mgr.GetClient(),
      Scheme:    mgr.GetScheme(),
      Recorder:  mgr.GetEventRecorderFor("packcontract-controller"),
      Resolver:  &contracts.OutputResolver{Reader: mgr.GetClient()},
      Projector: &contracts.Projector{Client: mgr.GetClient(), Scheme: mgr.GetScheme()},
      Config:    contractConfig,
  }.SetupWithManager(mgr)
    mgr.GetFieldIndexer().IndexField(PackContract, "spec.requires.from", ...)
    mgr.GetFieldIndexer().IndexField(PackContract, "spec.apps.name", ...)
```

The PackContract reconciler is a sibling of `NebariAppReconciler`, not a stage in its pipeline. It reads NebariApps but does not import the NebariApp sub-reconcilers.

### Test approach and seams

| Boundary | Seam | Real/fake | Level | Proves |
|---|---|---|---|---|
| Grant evaluation | `IsGranted` pure function | real | unit (table) | names, selectors, wildcard on/off, unmanaged namespaces never match |
| Version ranges | `InRange` | real | unit (table) | in range, out of range, malformed range, prerelease handling |
| Output resolution | `OutputResolver.Reader` | fake client | unit (table) | each source kind, missing sources, restricted values never in status |
| Projection | `Projector.Client` | fake client | unit (table) | create, update on change, hash annotation, prune on revoke, `TargetConflict` on foreign object |
| Controller wiring | `PackContractReconciler` with manager | envtest | integration | watches fan out, field indexes, owner-reference GC, status subresource |
| CEL validation | CRD admission | envtest | integration | exactly-one-source, exactly-one-target, restricted-only grants rejected |
| End-to-end journeys | Kind cluster | real | e2e | Journeys rows below |

## Open questions

1. **Grant location.** Grants live on the producer's contract and come from the producer's chart values. If platform admins need to grant without touching a pack's values, a separate grant object would be the escape hatch. Do we expect that need?
2. **NIC's `nebari-integration.yaml`.** It holds render-time required values today. Should it also list a pack's contract outputs, so NIC can show them before install, or is the in-cluster CRD enough?

## Follow-ups

- Make NebariApp's `hostname` and `service` optional, with CEL requiring them when routing, auth or the landing page is enabled, if a pack with no routable component appears.
- Network and identity outputs (pod selector labels, service account) and optional generation of NetworkPolicies from requirements.
- An RBAC-only output mode for consumers that read Secrets through the Kubernetes API.
- A platform RFD to add contracts to the pack specification once real packs have used them.
- `ValidateService` reports the existence and ports of Services in any namespace (`internal/controller/reconcilers/core/reconciler.go:118-155`). It should check a consent signal before reading cross-namespace Services, and the docs that say the Service must be in the same namespace (`docs/reconcilers/validation.md:75`, `docs/reconcilers/routing.md:350`) should match the code.

## Journeys

| # | Item | Proof | Check method | Evidence |
|---|------|-------|--------------|----------|
| 1 | A pack author can declare a contract with plain and restricted outputs in their chart, and it is discoverable once installed | A chart using `nebari-app.packContract` installs a contract; `kubectl get contracts -A` lists it with Version and Ready=True; `status.outputs` shows plain values and shows the restricted output only as `restricted: true, resolved: true` | automated: e2e `packcontract_test.go` "producer publishes outputs" (helm install of a fixture chart, then assertions on `kubectl get -o json`) | |
| 2a | A contract cannot reference a NebariApp in another namespace | The CRD has no namespace field on `apps[]` entries; a manifest with `apps: [{name: x, namespace: y}]` is rejected by the API server | automated: envtest "rejects unknown field on apps" (strict decoding) | |
| 2b | A contract whose NebariApp does not exist is not Ready | Contract with `apps: [{name: missing}]` reports `AppsReady=False / AppNotFound` and `Ready=False`; creating the NebariApp flips both to True | automated: envtest "missing app" | |
| 3 | Plain outputs drawn from a NebariApp appear without restating them | `fromApp: {field: url}` and `{field: clientId}` resolve to `https://<hostname>` and the same client ID the operator provisioned in Keycloak (`naming.ClientID`) in `status.outputs` | automated: unit table for `OutputResolver` plus e2e assertion in row 1's test | |
| 4 | dask-gateway-style consumer in its own namespace uses the producer's token through a same-namespace `secretKeyRef` once granted | Two namespaces; the producer grants the consumer namespace; a consumer pod with `env.valueFrom.secretKeyRef` to the projected Secret prints the producer's token value | automated: e2e "cross-namespace projection" (pod logs compared with the source Secret) | |
| 5 | A consumer that asks for an ungranted restricted output gets nothing, and its contract says why | No grant: no Secret in the consumer namespace; requirement state `NotGranted`; `RequirementsSatisfied=False` for a required requirement | automated: e2e "not granted" | |
| 6 | Source changes propagate and hand edits are overwritten | Patch the source Secret: the copy's value and `nebari.dev/contract-value-hash` change within 30s; edit the copy by hand: the operator restores it within 30s | automated: e2e "sync and drift" | |
| 7 | Revoking a grant or removing the requirement deletes the copy | Remove the consumer namespace from `allowedConsumers`: copy deleted, state `NotGranted`; restore the grant, then remove the requirement: copy deleted | automated: e2e "revocation" | |
| 8 | A disappearing producer never deletes the consumer's copy | Delete the producer contract: copy still present with its last value, state `Stale`; delete the source Secret: same | automated: e2e "producer outage" | |
| 9 | A version pin stops updates on a major bump | Consumer pins `>=1.0.0 <2.0.0`; producer moves to `2.0.0` and changes the source: copy keeps the old value, state `VersionMismatch` | automated: e2e "version mismatch" plus unit table for `InRange` | |
| 10 | Wildcard grants work only when allowed, and are off by default | With the setting unset, an empty-selector grant projects nothing and the producer shows `GrantsReady=False / WildcardGrantsDisabled`; with it true, the copy appears, the producer shows `wildcardGrant: true`, and a Warning event is recorded | automated: unit table for `IsGranted` plus envtest with both config values; e2e runs with the dev setting on | |
| 11 | Same-namespace install works with no copy | Producer and consumer in one namespace, no grant: no projected copy exists, the consumer's requirement is `Satisfied`, the producer's status shows the source Secret name and key, and a consumer pod reading that Secret directly prints the token | automated: e2e "same namespace" | |
| 12 | A spin can wire two packs at bootstrap using only values, reproducible by hand | Render a two-pack fixture spin with `helm template`; the output contains only the producer grant and the consumer requires block as wiring; applying the rendered YAML by hand yields row 4's result | narrated: `helm template` transcript plus `kubectl get secret` in the consumer namespace | |
