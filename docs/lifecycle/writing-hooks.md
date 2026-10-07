# Writing cleanup hooks

> **Part of:** [Lifecycle API](README.md)
> **Audience:** software pack authors

A `UserCleanupHook` tells the operator what to run when a user is deleted in Keycloak. The operator wraps your pod template in a Job, in your namespace, under your ServiceAccount, and tells it which user through environment variables. You write the cleanup, the operator decides when it runs.

## The hook

```yaml
apiVersion: lifecycle.nebari.dev/v1alpha1
kind: UserCleanupHook
metadata:
  name: hub-delete
  namespace: data-science
spec:
  stage: delete
  template:
    metadata:
      labels:
        hub.jupyter.org/network-access-hub: "true"
    spec:
      serviceAccountName: hub-user-cleanup
      restartPolicy: Never
      containers:
        - name: cleanup
          image: alpine/k8s:1.34.12
          command: [sh, -c, 'kubectl delete pvc "claim-$NEBARI_KEYCLOAK_USERNAME" --ignore-not-found']
```

`stage` is one of:

- `disable`. Runs as soon as the operator picks up the deletion, within one poll interval. For things that should stop right away: running servers, sessions, scheduled work. Should be reversible.
- `delete`. Runs after the grace period, 30 days by default. For things that are gone for good: storage, database rows, the user's records in your service.

One stage per hook. A pack that needs both registers two hooks.

`template` is a pod template. `metadata` accepts labels and annotations, `spec` is a regular `PodSpec`. `restartPolicy` must be `Never` or `OnFailure` and defaults to `Never`. The remaining fields tune the Job and have defaults: `backoffLimit` (3), `activeDeadlineSeconds` (900), `ttlSecondsAfterFinished` (7 days). `dryRun: true` makes the operator run your Job with the dry-run flag set, see below, without changing anything else.

Field by field reference: [api-reference.md](../api-reference.md).

## What your Job receives

The operator appends these to the `env` of every container and init container. Anything you set yourself is kept.

| Variable | Value |
| --- | --- |
| `NEBARI_KEYCLOAK_USER_ID` | Keycloak user id, same as the OIDC `sub` claim |
| `NEBARI_KEYCLOAK_USERNAME` | Username. Empty when Keycloak did not record it |
| `NEBARI_KEYCLOAK_DELETED_AT` | When the user was deleted, RFC 3339 |
| `NEBARI_CLEANUP_STAGE` | `disable` or `delete` |
| `NEBARI_CLEANUP_DRY_RUN` | `"true"` when the hook should log what it would do and change nothing |
| `NEBARI_USER_DELETION` | Name of the `UserDeletion` this Job belongs to, for log correlation |

That is the whole contract. There is no mounted file and no Kubernetes object to read, so the hook can be tested locally by exporting the six variables. Key your cleanup on the user id where you can, the username can be empty and usernames can be reused.

Jobs also carry three labels for finding them: `lifecycle.nebari.dev/user-id`, `lifecycle.nebari.dev/hook`, `lifecycle.nebari.dev/stage`.

## Where it runs

In the hook's namespace, under the ServiceAccount in the template. The operator never runs your code with its own identity. Create the ServiceAccount, Role, and RoleBinding your cleanup needs alongside the hook. The data-science-pack ships all four in one template.

The namespace must be labeled `nebari.dev/managed=true`, the same opt-in the operator requires for a `NebariApp`. Whoever provisions namespaces sets it. A Helm chart cannot label its own release namespace. On NIC, set it through `managedNamespaceMetadata` on the ArgoCD Application.

## Validation

When you create or change a hook the operator renders a Job from it, submits it as a dry-run, and checks that the ServiceAccount exists and the namespace is managed. The result is the `Accepted` condition:

```bash
kubectl get usercleanuphooks -n data-science
kubectl describe usercleanuphook hub-delete -n data-science   # condition message and Events
```

`Accepted=True` means the Job object is valid. It does not mean the pod will run. The dry-run never creates a pod, so these only show up on the first real deletion:

- Image pull errors, a missing pull secret
- Pod Security Admission and other admission policies on pods
- Secrets and ConfigMaps referenced by env vars or volumes
- Resource quotas, node selectors, scheduling
- What the ServiceAccount is allowed to do, only that it exists is checked
- The script itself

Set `dryRun: true` on a new hook and delete a test user to exercise the whole path without side effects, then remove it.

Validation runs once per spec change. If the hook was rejected because the ServiceAccount or the namespace label was missing, creating them later does not re-check the hook. Change its spec, an annotation is enough.

## Failure behavior

Your Job fails the way any Job does: a non-zero exit retries up to `backoffLimit`, then the entry on the marker is `Failed` with the Job's reason. A hang ends at `activeDeadlineSeconds`. Keep that in mind for network calls, a `curl` without `--max-time` turns a blocked connection into a fifteen minute wait. Logs stay for `ttlSecondsAfterFinished`.

A failed entry is not retried by the operator. A hook you fix afterwards applies to deletions that come after it. Markers whose entry for your hook already failed keep that outcome.

If your hook is rejected after an upgrade, markers that had recorded it wait with the entry `Pending` until the hook is accepted again. They do not complete without it.

## Checking what ran

```bash
kubectl get userdeletions                                    # one per deleted user
kubectl get userdeletions <user-id> -o yaml                  # status.hooks has one entry per hook
kubectl logs -n data-science -l lifecycle.nebari.dev/stage=disable
```
