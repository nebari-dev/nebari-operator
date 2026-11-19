# ADR-004: Provision managed PostgreSQL databases through the NebariApp CRD

| Field       | Value                               |
|-------------|-------------------------------------|
| Date        | 2026-11-16                          |
| Status      | **Proposed**                        |
| Deciders    | NIC maintainers                     |
| Supersedes  | The unrecorded July 2026 decision to create databases with GitOps manifests, which closed https://github.com/nebari-dev/nebari-operator/pull/151 |

## Context

Software packs that need PostgreSQL used to inherit it from upstream Helm charts that bundle Bitnami sub-charts. Since August 2025 those images are behind a paywall and the free mirrors are frozen, so that route is closed. https://github.com/nebari-dev/nebari-infrastructure-core/issues/303 proposed [CloudNativePG](https://cloudnative-pg.io) (CNPG) as foundational database infrastructure, with packs requesting a database through the `NebariApp` resource the same way they request a Keycloak client.

NIC now installs the CNPG operator on every GitOps bootstrap (https://github.com/nebari-dev/nebari-infrastructure-core/pull/455). What was left open is who creates the per-pack CNPG `Cluster`.

A first implementation, https://github.com/nebari-dev/nebari-operator/pull/151, added a single `spec.database` block with `enabled` and `provider` fields. It was closed on 2026-07-28 in favor of creating databases with manifests in the GitOps repository. That decision was never written down as an ADR.

The pull request was reopened on 2026-11-16. The reason recorded there is that users and software pack authors need a way to automate creating databases, which hand-written manifests in the GitOps repository do not give them.

The discussion on the NIC issue describes what happens without a platform-level request path: every contributor with a PostgreSQL-backed pack picks a provider, a hosting location, and a provisioning workflow independently, and access to add a database routes back to whoever set up the shared instance.

## Decision

Packs request managed PostgreSQL databases through `spec.databases` on the `NebariApp`, and the operator provisions them on CNPG.

```yaml
spec:
  databases:
    - name: metadata
    - name: warehouse
      instances: 3
      size: 20Gi
```

Key choices:

- **A list, with one CNPG `Cluster` per entry.** `instances` and `size` are cluster-level settings, so an entry maps to a Cluster, not to a logical database inside a shared server. A pack that needs differently sized or isolated databases can ask for them; the cost is that every entry is a full PostgreSQL cluster.
- **No `enabled` and no `provider`.** An entry's presence is the request. CNPG is the only backend; a `provider` field can be added later as an optional, defaulted field without breaking any manifest.
- **A spec edit never destroys data.** Removing an entry stops management and reports the database as retained. Only deleting the `Cluster` by hand, or deleting the `NebariApp`, removes it. Helm replaces lists wholesale, so a values overlay can drop an entry without anyone intending it; that must not cost a database. The operator's RBAC carries no `delete` verb on CNPG Clusters.
- **`name` names Kubernetes resources, not PostgreSQL objects.** The Cluster is `<app>-<name>-db` and the credentials Secret is `<app>-<name>-db-credentials`. The database and its owner keep CNPG's defaults, and packs read them from the Secret. Entry names allow hyphens, which PostgreSQL identifiers do not without quoting.
- **Credentials are normalized.** The operator copies CNPG's generated Secret into a Secret with a fixed key set (`host`, `port`, `username`, `password`, `database`, `uri`), so the pack-facing contract does not depend on CNPG's key names.
- **CNPG stays optional to the operator.** There is no startup watch on CNPG types. On a cluster without the CRDs the database subsystem reports `CNPGNotInstalled` and the rest of the app reconciles normally.
- **The operator never adopts a Cluster it did not create.** A name collision is reported, not resolved by taking over someone else's database.
- **A single-instance database has no PodDisruptionBudget.** CNPG's budget keeps the primary from being evicted, which with one instance protects nothing and makes the node impossible to drain (https://github.com/nebari-dev/nebari-infrastructure-core/issues/724). The operator disables it for one instance and keeps it for two or more, as CNPG recommends. The cost is that a node drain takes a single-instance database down until its pod is running again.

Behavior is specified in [`docs/reconcilers/database.md`](../reconcilers/database.md), and the pack-facing requirements in [`docs/pack-specification.md`](../pack-specification.md).

## Consequences

### Positive

- A pack gets a database with the same self-service shape as an OIDC client: declare it, read a Secret. No person in the loop for the routine case.
- One request path for the platform, so databases are discoverable from the `NebariApp` resources instead of from whoever set each one up.
- Packs can drop their Bitnami PostgreSQL sub-charts.

### Negative

- The operator gains a fourth concern and a dependency on the CNPG API types (`github.com/cloudnative-pg/api`), pinned to the release that matches the operator's Kubernetes libraries.
- Retained databases accumulate until someone deletes them. That is the price of never deleting on a spec edit.
- The feature surface is deliberately small. Backups, connection pooling, PostgreSQL version selection, and extensions are not exposed.

### Neutral

- This is a default, not a lock-in. A pack with a reason to use a database elsewhere (managed cloud service, hosted DBaaS, its own Cluster manifest in the GitOps repository) is free to, and simply does not set `spec.databases`.

## Alternatives considered

### Cluster manifests in the GitOps repository

Each pack, or the platform's GitOps repository, carries its own CNPG `Cluster` manifest. This was the decision that closed the first implementation. It needs no operator code and exposes all of CNPG. It also leaves every pack to choose names, credentials wiring, and access scoping on its own, which is the drift the `NebariApp` contract exists to prevent.

### One shared Cluster with a logical database per entry

Cheaper per database, using CNPG's `Database` resource. Rejected for now: per-entry `instances` and `size` would be meaningless, and packs would share a failure domain and a superuser. It remains available as a later, additive option.

### Keep the single `spec.database` block

Simpler, and enough for most packs. Rejected because moving from a single object to a list later is a breaking change to the CRD, while a list of one costs nothing today.
