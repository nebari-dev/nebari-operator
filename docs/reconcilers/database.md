# Database Reconciler

> **Part of:** [Reconciler Architecture](README.md) **Phase:** 4 of 4 (Validation → Routing → Authentication → Database)
> **Purpose:** Provision managed PostgreSQL databases through CloudNativePG

## Overview

The database reconciler provisions a managed PostgreSQL database for a
NebariApp that sets `spec.database.enabled: true`. It creates a
[CloudNativePG](https://cloudnative-pg.io) `Cluster`, waits for it to become
ready, publishes connection credentials in a well-known Secret, and scopes
read access to the app's ServiceAccount. It is the NebariApp-side contract of
the platform database infrastructure discussed in
[https://github.com/nebari-dev/nebari-infrastructure-core/issues/303](https://github.com/nebari-dev/nebari-infrastructure-core/issues/303);
the CloudNativePG operator itself is installed by NIC on every GitOps
bootstrap as foundational infrastructure (added in
https://github.com/nebari-dev/nebari-infrastructure-core/pull/455).

The logic is encapsulated in the `DatabaseReconciler` located at
`internal/controller/reconcilers/database/`.

## Architecture

```
NebariAppReconciler
  └-> DatabaseReconciler.ReconcileDatabase()
       ├-> ValidateDatabaseClusterName() - Enforce CNPG's 50-char name cap
       ├-> reconcileCluster() - Create/update the CNPG Cluster "<name>-db"
       ├-> readiness poll - Requeue until the Cluster's Ready condition is True
       ├-> reconcileCredentialsSecret() - Normalize CNPG's "<name>-db-app"
       │    Secret into "<name>-db-credentials"
       └-> reconcileSecretRBAC() - Role/RoleBinding scoping the Secret to the
            app's ServiceAccount
```

A Secrets watch maps CloudNativePG's generated `<name>-db-app` Secret back to
its NebariApp, so password rotations refresh the normalized copy without
waiting for the periodic requeue.
The watch is cluster-wide over Secrets (filtered by name in the handler);
the operator's cached client already maintains a Secrets informer for the
auth flow, so this adds no new cache class.

## Spec

```yaml
spec:
  database:
    enabled: true
    provider: cloudnativepg   # default and only supported value
    instances: 1              # default 1, maximum 9
    size: 1Gi                 # default 1Gi; cannot be decreased
```

The `size` field's positivity check uses CEL's `quantity()` library, which
requires Kubernetes 1.29 or newer to install the CRD.

## Credentials contract

The operator writes Secret `<name>-db-credentials` in the NebariApp's
namespace:

| Key        | Meaning                                         |
| ---------- | ----------------------------------------------- |
| `host`     | In-namespace hostname of the read-write service |
| `port`     | PostgreSQL port                                 |
| `username` | Application user                                |
| `password` | Application user password                       |
| `database` | Database name                                   |
| `uri`      | Full PostgreSQL connection URI                  |

Reference it from pod env via `secretKeyRef`/`envFrom`. Read access is
restricted to the app's ServiceAccount (`spec.serviceAccountName`, defaulting
to the NebariApp's name).

## Conditions

| Condition       | Reason                 | Meaning                                           |
| --------------- | ---------------------- | ------------------------------------------------- |
| `DatabaseReady` | `DatabaseProvisioning` | Cluster or credentials not ready yet (polling)    |
| `DatabaseReady` | `Available`            | Database ready, credentials published             |
| `DatabaseReady` | `CNPGNotInstalled`     | CloudNativePG CRDs missing on this cluster        |
| `DatabaseReady` | `DatabaseDisabled`     | Toggle turned off while the database still exists |
| `DatabaseReady` | `Failed`               | Provisioning error (see message and events)       |

## Lifecycle and data safety

- **Disabling never deletes.** Setting `enabled: false` stops management and
  sets `DatabaseReady=False/DatabaseDisabled`; the Cluster, its data, and the
  credentials Secret remain. Delete the `Cluster` resource yourself to remove
  the database and its data. Orphan reporting relies on the NebariApp's
  status carrying evidence of the earlier provisioning; a manually wiped
  status skips the warning (the data is still retained).
- **Deleting the NebariApp deletes the database.** All database resources are
  owner-referenced to the NebariApp and are garbage collected with it.
- **No CNPG, no failure.** On clusters without CloudNativePG the rest of the
  NebariApp reconciles normally; the database subsystem reports
  `CNPGNotInstalled` and re-checks every 5 minutes.
