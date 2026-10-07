# Lifecycle API

> **API group:** `lifecycle.nebari.dev/v1alpha1`
> **Purpose:** Run cleanup registered by software packs when a user is deleted in Keycloak

This page is for whoever runs the operator. Pack authors should read [Writing cleanup hooks](writing-hooks.md).

## Overview

When a user is deleted in Keycloak, the operator runs cleanup Jobs that software packs registered ahead of time. Keycloak records the deletion as an admin event, packs say what to run, the operator connects the two. Two kinds take part:

- `UserCleanupHook` (namespaced). A pack registers one per piece of per-user state it owns. It holds a pod template and a stage, `disable` or `delete`.
- `UserDeletion` (cluster-scoped). One per deleted user, named after the Keycloak user id. It records which hooks ran and how they ended. Also called the marker.

Three pieces in the operator, all under `internal/controller/lifecycle/`:

```
KeycloakDeletionPoller          every poll interval
  └─> Keycloak admin events  -> create UserDeletion per event, advance cursor

UserCleanupHookReconciler       on hook create or spec change
  └─> dry-run Job, check ServiceAccount and namespace -> Accepted condition

UserDeletionReconciler          on marker, Job, or hook change
  ├─> discoverHooks   one entry per eligible hook
  ├─> observeJobs     copy Job outcomes onto entries
  ├─> createDueJobs   disable now, delete after dueAt
  └─> summarize       phase, conditions, completedAt
```

The lifecycle controllers only start when `KEYCLOAK_ENABLED` is true. They share the operator's Keycloak admin credentials for now.

## Operator configuration

Environment variables on the operator Deployment, next to the `KEYCLOAK_*` ones. NIC sets them through its deployment patch.

| Variable | Default | Meaning |
| --- | --- | --- |
| `LIFECYCLE_POLL_INTERVAL` | `5m` | How often the poller asks Keycloak for new deletion events |
| `LIFECYCLE_GRACE_PERIOD` | `720h` (30 days) | Time between the Keycloak deletion and the `delete` stage |
| `LIFECYCLE_EVENT_RETENTION` | `168h` (7 days) | How long Keycloak keeps admin events. Used as the lookback when there is no cursor and as the retry window for Job creation. Must not exceed Keycloak's expiration |
| `LIFECYCLE_MARKER_RETENTION` | `2160h` (90 days) | How long a completed marker is kept as a tombstone. Must be at least the event retention |
| `LIFECYCLE_CURSOR_CONFIGMAP_NAME` | `user-deletion-cursor` | ConfigMap holding the last processed event time |
| `LIFECYCLE_CURSOR_CONFIGMAP_NAMESPACE` | `nebari-operator-system` | Namespace of that ConfigMap |

Durations use Go syntax, so `30d` is not valid, write `720h`.

Two constraints hold the design together. The marker retention must cover the event retention, otherwise Keycloak can replay an event after its tombstone is gone and cleanup runs twice. And the grace period is frozen on each marker when it is created, so changing it only affects deletions picked up afterwards.

## Detection

The poller reads `GET /admin/realms/<realm>/admin-events?resourceTypes=USER&operationTypes=DELETE` since the cursor. NIC enables admin events on the realm. Without a cursor, on first run or after the ConfigMap was deleted, it looks back over the event retention window. For each event it creates a `UserDeletion` named after the user id. `AlreadyExists` is ignored, which is what makes a replayed event harmless while the tombstone exists. The cursor advances to the newest event seen, with millisecond precision, and only once every event in the batch was handled.

The poller runs on the leader only.

## Marker lifecycle

```
Pending ──> InProgress ──> Completed ──> deleted after MarkerRetention
```

On the first pass the reconciler sets `status.dueAt` to `deletedAt` plus the grace period and moves to `InProgress`. Then on every pass:

1. **Discover.** Every hook that is `Accepted=True` and lives in a namespace labeled `nebari.dev/managed=true` gets an entry under `status.hooks`, state `Pending`. An entry whose hook no longer exists is marked `Skipped`. An entry whose hook still exists but is currently not accepted, or whose namespace lost its label, stays `Pending` until the hook is eligible again.
2. **Observe.** For every `Running` entry, the Job is read. `Complete` becomes `Succeeded`, `Failed` becomes `Failed` with the Job's reason. A Job that is missing for longer than a minute becomes `Failed` with `JobLost`.
3. **Create.** `disable` entries get their Job at once, `delete` entries once `dueAt` has passed. Job names are derived from user id, hook, and stage, so a retry after a crash adopts the Job instead of creating a second one. A Job the API server rejects marks the entry `Failed` with `JobCreateRejected`. A transient error is retried every 30 seconds for the event retention window, then `JobCreateTimedOut`.
4. **Summarize.** The marker completes when `dueAt` has passed and every entry is terminal. With no hooks it completes at `dueAt` with reason `NoHooks`.

A marker with a `Pending` entry never completes. A hook that stays broken keeps every open marker that recorded it in `InProgress`, visible as `HooksSucceeded=False` with reason `HooksPending`. Fix or remove the hook to let them finish.

Completed markers stay as tombstones for the marker retention, counted from `completedAt`, then the reconciler deletes them. A finalizer, `lifecycle.nebari.dev/in-flight-jobs`, holds a marker that is deleted by hand while one of its Jobs is still running.

Deleting a marker by hand is not cancel and not retry. The poller only recreates it if its event is still at the cursor, which is only true for the most recent deletion.

## Conditions and reasons

### UserCleanupHook

| Condition | Status | Reason | Meaning |
| --- | --- | --- | --- |
| `Accepted` | True | `TemplateValid` | The rendered Job passed API server validation and the ServiceAccount exists |
| `Accepted` | False | `TemplateInvalid` | The API server rejected the rendered Job. The message carries its error |
| `Accepted` | False | `ServiceAccountMissing` | The template names a ServiceAccount that does not exist in the hook's namespace |
| `Accepted` | False | `NamespaceNotManaged` | The namespace is not labeled `nebari.dev/managed=true` |
| `Accepted` | Unknown | `ValidationUnavailable` | The dry-run or a lookup failed for another reason. Retried every minute |

Validation runs once per spec generation. A `False` verdict is not re-evaluated when the ServiceAccount or the label appear later, change the hook's spec to trigger it. Each verdict is also emitted as a Kubernetes Event on the hook.

### UserDeletion

| Condition | Status | Reason | Meaning |
| --- | --- | --- | --- |
| `IdentifiersComplete` | True | `UsernameKnown` | The admin event carried the username |
| `IdentifiersComplete` | False | `UsernameMissing` | Keycloak did not record the username, hooks keyed on it may do nothing |
| `HooksSucceeded` | True | `AllSucceeded` | Every entry is `Succeeded` |
| `HooksSucceeded` | True | `NoHooks` | No hook was registered when the marker completed |
| `HooksSucceeded` | False | `HooksPending` | At least one entry is `Pending` or `Running` |
| `HooksSucceeded` | False | `HookFailed` | At least one entry is `Failed` |
| `HooksSucceeded` | False | `HookSkipped` | At least one entry is `Skipped` and none failed |

Per-entry states under `status.hooks`: `Pending`, `Running`, `Succeeded`, `Failed`, `Skipped`. Reasons on failed or skipped entries: `JobLost`, `HookRemoved`, `JobCreateFailed`, `JobCreateRejected`, `JobCreateTimedOut`, or the reason copied from the Job's `Failed` condition.

## RBAC

The operator needs `create` on `batch/jobs` in every namespace with a hook, `get` on namespaces and ServiceAccounts, `create` on `userdeletions` for the poller, and `get`, `create`, `update` on its cursor ConfigMap. The generated role is in `config/rbac/role.yaml`. Markers come from the `+kubebuilder:rbac` comments on the two reconcilers and the poller.

## Open questions

Tracked on nebari-dev/nebari-operator#188. Cancel and retry as explicit marker actions, re-evaluating a rejected hook when its namespace or ServiceAccount appear, a separate Deployment with a read-only Keycloak client, and username reuse through an upstream identity provider.
