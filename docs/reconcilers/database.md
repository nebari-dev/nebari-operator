# Database Reconciler

> **Part of:** [Reconciler Architecture](README.md) **Phase:** 4 of 4 (Validation → Routing → Authentication → Database)
> **Purpose:** Provision managed PostgreSQL databases through CloudNativePG

## Overview

The database reconciler provisions the managed PostgreSQL databases a
NebariApp lists under `spec.databases`. For each entry it creates a
[CloudNativePG](https://cloudnative-pg.io) `Cluster`, waits for it to become
ready, and publishes connection credentials in a well-known Secret. Read
access to those Secrets is scoped to the app's ServiceAccount. It is the
NebariApp-side contract of the platform database infrastructure discussed in
[https://github.com/nebari-dev/nebari-infrastructure-core/issues/303](https://github.com/nebari-dev/nebari-infrastructure-core/issues/303);
the CloudNativePG operator itself is installed by NIC on every GitOps
bootstrap as foundational infrastructure (added in
https://github.com/nebari-dev/nebari-infrastructure-core/pull/455). The
decision to provision through the operator is recorded in
[ADR-004](../decisions/2026-11-16-managed-databases-via-nebariapp.md).

The logic is encapsulated in the `DatabaseReconciler` located at
`internal/controller/reconcilers/database/`.

## Architecture

```
NebariAppReconciler
  └-> DatabaseReconciler.ReconcileDatabase()
       ├-> listOwnedClusters() - Find every Cluster this NebariApp controls
       ├-> for each spec.databases entry: reconcileEntry()
       │    ├-> ValidateDatabaseClusterName() - Enforce CNPG's 50-char name cap
       │    ├-> reconcileCluster() - Create/update Cluster "<app>-<name>-db"
       │    ├-> readiness check - Is the Cluster's Ready condition True?
       │    └-> reconcileCredentialsSecret() - Normalize CNPG's
       │         "<app>-<name>-db-app" Secret into "<app>-<name>-db-credentials"
       ├-> retained clusters - Owned Clusters with no entry are reported, not touched
       ├-> reconcileSecretRBAC() - One Role/RoleBinding scoping every published
       │    credentials Secret to the app's ServiceAccount
       └-> summarize() - Fold the per-entry outcomes into DatabaseReady
```

Entries are reconciled independently: a database that is still provisioning,
or that cannot be provisioned at all, does not hold back the others. Their
credentials are published and readable as soon as they are ready.

Credentials are published for the first time only once the Cluster is Ready.
After that they are kept current even while the Cluster is temporarily not
Ready (adding a replica, a rolling update, a failover), because the database
usually keeps serving through those and a password rotated meanwhile must
still reach the app. `DatabaseReady` is `False` for as long as CloudNativePG
reports the Cluster not Ready, including during such changes to a database
that was ready before.

A Secrets watch maps CloudNativePG's generated `<app>-<name>-db-app` Secret
back to its NebariApp, so password rotations refresh the normalized copy
without waiting for the periodic requeue. The watch is cluster-wide over
Secrets (filtered by name suffix in the handler, then matched against the
NebariApps in the Secret's namespace); the operator's cached client already
maintains a Secrets informer for the auth flow, so this adds no new cache
class. There is deliberately no startup watch on CNPG types: the CRDs are
optional infrastructure, and a watch on a missing CRD would stop the manager
from starting. Cluster readiness is polled every 15 seconds instead.

## Spec

```yaml
spec:
  databases:
    - name: metadata        # required, unique within the list
      instances: 1          # default 1, maximum 9
      size: 1Gi             # default 1Gi; cannot be decreased
    - name: warehouse
      instances: 3
      size: 20Gi
```

| Field       | Rules                                                                        |
| ----------- | ---------------------------------------------------------------------------- |
| `name`      | Required. Lowercase DNS label, at most 20 characters, unique within the list |
| `instances` | 1 to 9, default 1. One primary plus N-1 replicas                             |
| `size`      | Kubernetes quantity greater than zero, default `1Gi`. Per-instance storage   |

At most 10 databases can be requested per NebariApp. **Every entry is a full
PostgreSQL cluster** with its own pods and volumes, not a logical database
inside a shared server.

`name` identifies the entry and the Kubernetes resources created for it. It
does not name anything inside PostgreSQL: the database and its owner keep
CloudNativePG's defaults (`app`), and a pack reads the real values from the
credentials Secret.

The `size` field's positivity check uses CEL's `quantity()` library, which
requires Kubernetes 1.29 or newer to install the CRD.

## Resource names

| Resource                 | Name                          | Created by    |
| ------------------------ | ----------------------------- | ------------- |
| CNPG `Cluster`           | `<app>-<name>-db`             | The operator  |
| Credentials `Secret`     | `<app>-<name>-db-credentials` | The operator  |
| `Role` and `RoleBinding` | `<app>-db-secret-reader`      | The operator  |
| CNPG app-user `Secret`   | `<app>-<name>-db-app`         | CloudNativePG |

CloudNativePG caps Cluster names at 50 characters, so `<app>-<name>-db` must
fit within that. An entry that does not is reported on the condition and no
Cluster is created for it.

The operator never adopts a Cluster it did not create. If a Cluster already
exists under the name an entry needs and this NebariApp does not control it,
the entry is reported as failed and the Cluster is left untouched. This also
covers two NebariApps in one namespace whose names concatenate to the same
string (app `foo` with database `bar-baz`, and app `foo-bar` with database
`baz`).

## Credentials contract

For each database the operator writes Secret `<app>-<name>-db-credentials` in
the NebariApp's namespace:

| Key        | Meaning                                         |
| ---------- | ----------------------------------------------- |
| `host`     | In-namespace hostname of the read-write service |
| `port`     | PostgreSQL port                                 |
| `username` | Application user                                |
| `password` | Application user password                       |
| `database` | Database name                                   |
| `uri`      | Full PostgreSQL connection URI                  |

Reference it from pod env via `secretKeyRef`/`envFrom`:

```yaml
env:
  - name: DATABASE_URL
    valueFrom:
      secretKeyRef:
        name: superset-metadata-db-credentials
        key: uri
```

Read access through the Kubernetes API is restricted to the app's
ServiceAccount (`spec.serviceAccountName`, defaulting to the NebariApp's
name).

## Status

```yaml
status:
  conditions:
    - type: DatabaseReady
      status: "True"
      reason: Available
  databases:
    - name: metadata
      secretRef:
        name: superset-metadata-db-credentials
        namespace: analytics
    - name: warehouse
      secretRef:
        name: superset-warehouse-db-credentials
        namespace: analytics
```

`status.databases` has one entry per `spec.databases` entry, in the same
order, followed by one entry per retained database (see below) with
`retained: true`. `secretRef` is set once that database's credentials have
been published.

## Conditions

`DatabaseReady` covers every requested database at once. It is absent when no
database is requested and none is retained.

| Condition       | Reason                 | Meaning                                                              |
| --------------- | ---------------------- | -------------------------------------------------------------------- |
| `DatabaseReady` | `Available`            | Every requested database is ready and its credentials are published  |
| `DatabaseReady` | `DatabaseProvisioning` | At least one Cluster or its credentials are not ready yet (polling)  |
| `DatabaseReady` | `CNPGNotInstalled`     | CloudNativePG CRDs missing on this cluster                           |
| `DatabaseReady` | `DatabaseRetained`     | No database is requested, but earlier ones still exist               |
| `DatabaseReady` | `Failed`               | An entry cannot be provisioned (see message and events)              |

The message names each database that is not ready and why. While
`DatabaseReady` is `False` for a requested database, the aggregate `Ready`
condition is `False` with the same reason, even though routing and auth are
already in place.

## Events

| Event                     | Type    | When                                                       |
| ------------------------- | ------- | ---------------------------------------------------------- |
| `DatabaseProvisioned`     | Normal  | A database becomes ready for the first time                |
| `DatabaseProvisionFailed` | Warning | An entry fails or is blocked (once per distinct failure)   |
| `DatabaseRetained`        | Warning | An entry was removed while its database still exists       |

## Lifecycle and data safety

- **Removing an entry never deletes.** The operator stops managing that
  Cluster and reports it under `status.databases` with `retained: true`. The
  Cluster, its data, and the credentials Secret remain, and the app keeps its
  read access to that Secret, so a list accidentally replaced by a Helm
  values overlay does not take a running app's data away. The retained
  Cluster is no longer resized and its credentials Secret is no longer
  refreshed. Operator RBAC has no `delete` verb on CNPG Clusters.
- **Adding the entry back resumes management.** The Cluster is still owned by
  the NebariApp, so an entry with the same name picks it up again.
- **Renaming an entry creates a new database.** A rename is a removal plus an
  addition: the old database is retained and a new, empty one is provisioned
  under the new name.
- **Removing a retained database is manual.** Delete the `Cluster` resource
  yourself to remove the database and its data.
- **Deleting the NebariApp deletes its databases.** Every database resource,
  retained ones included, is owner-referenced to the NebariApp and is garbage
  collected with it.
- **No CNPG, no failure of the rest.** On clusters without CloudNativePG,
  routing, TLS, and auth reconcile normally; the database subsystem reports
  `CNPGNotInstalled` and re-checks every 5 minutes.
