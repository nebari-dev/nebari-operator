
### Application Not Accessible

**Check NebariApp status**:
```bash
kubectl describe nebariapp <name> -n <namespace>
```

**Common issues**:
- Namespace not labeled: `kubectl label namespace <ns> nebari.dev/managed=true`
- Service not found: Verify service exists and matches spec
- Gateway not ready: Check Gateway status in `envoy-gateway-system`

### Certificate Issues

**Check certificate**:
```bash
kubectl get certificate -n envoy-gateway-system
kubectl describe certificate nebari-gateway-cert -n envoy-gateway-system
```

**Common issues**:
- cert-manager not installed
- ClusterIssuer not configured
- DNS not properly configured (for Let's Encrypt)

### User-Provided TLS Secret Issues

These apply when a NebariApp sets `routing.tls.secretName` (see [Configuration Reference](configuration-reference.md#routingtlssecretname)).

#### Symptom: `TLSReady=False` with reason `UserProvidedSecretNotFound`

**Cause:** `routing.tls.secretName` references a secret that does not exist in `envoy-gateway-system`.

**Fix:** Create the secret. The operator does not block reconciliation on the secret being present, so the per-app HTTPS listener is already attached and Envoy will pick the secret up as soon as it is created. Re-readiness today depends on the periodic requeue (~30 seconds).

```bash
kubectl create secret tls <secret-name> \
  -n envoy-gateway-system \
  --cert=path/to/tls.crt --key=path/to/tls.key
```

Verify:

```bash
kubectl get nebariapp <app> -n <ns> \
  -o jsonpath='{.status.conditions[?(@.type=="TLSReady")].reason}'
# Expect: UserProvidedSecretReady
```

#### Symptom: `TLSReady=False` with reason `UserProvidedSecretInvalidType`

**Cause:** A secret with the configured name exists in `envoy-gateway-system`, but it is not of type `kubernetes.io/tls`. Envoy Gateway will not load it.

**Fix:** Recreate the secret with the correct type:

```bash
kubectl delete secret <secret-name> -n envoy-gateway-system
kubectl create secret tls <secret-name> \
  -n envoy-gateway-system \
  --cert=path/to/tls.crt --key=path/to/tls.key
```

Confirm the secret type:

```bash
kubectl get secret <secret-name> -n envoy-gateway-system -o jsonpath='{.type}'
# Expect: kubernetes.io/tls
```

#### Symptom: `TLSReady=False` with reason `UserProvidedSecretCheckFailed`

**Cause:** The operator could not determine the secret's state. Typically a transient API server error, an etcd hiccup, or an RBAC failure (the operator's ServiceAccount cannot `get` Secrets in `envoy-gateway-system`).

**Fix:** Check the operator's recent logs and the events on the NebariApp; the message field on the condition includes the underlying error.

```bash
kubectl logs -n nebari-operator-system -l control-plane=controller-manager --tail=200 | grep -i secret
kubectl describe nebariapp <app> -n <ns>
```

If the cause is RBAC, confirm the operator has `secrets: get;list;watch` in `envoy-gateway-system` (the default install does).

#### Symptom: Switching from cert-manager to `secretName` left an old Certificate behind

**Cause:** None - the operator deletes its owned `Certificate` automatically on the next reconcile after `routing.tls.secretName` is added. If a `Certificate` remains, it either has different ownership labels (not deleted intentionally) or the migration reconcile has not yet run.

**Verify migration was clean:**

```bash
kubectl get certificate -n envoy-gateway-system \
  -l nebari.dev/nebariapp-name=<app>,nebari.dev/nebariapp-namespace=<ns>
# Expect: No resources found
```

If a Certificate persists with mismatched labels, it pre-dates this NebariApp's ownership and is left alone deliberately.

### Gateway Listener Conflicts

**Symptom:** NebariApp shows `TLSReady=False` with reason `GatewayListenerConflict`

**Error message:**
```
Gateway listener conflict: Multiple NebariApps cannot share hostname <hostname>
with per-app TLS. Set routing.tls.enabled=false to use shared wildcard listener,
or use unique hostnames.
```

**Root cause:** Multiple NebariApps are trying to create per-app HTTPS listeners for the same hostname. The Gateway API requires that port + protocol + hostname combinations be unique for each listener.

**Solutions:**

1. **Use shared wildcard HTTPS listener** (recommended for apps sharing a hostname):
   ```yaml
   spec:
     hostname: shared.example.com
     routing:
       tls:
         enabled: false  # Use Gateway's wildcard cert instead
   ```
   
2. **Use unique hostnames** (when apps need separate DNS names):
   ```yaml
   # App 1
   spec:
     hostname: app1.example.com
   
   # App 2
   spec:
     hostname: app2.example.com
   ```

**Check for conflicts:**
```bash
# Find all NebariApps using the same hostname
kubectl get nebariapp -A -o json | jq -r '.items[] | select(.spec.hostname=="<hostname>") | "\(.metadata.namespace)/\(.metadata.name)"'

# Check Gateway listeners
kubectl get gateway nebari-gateway -n envoy-gateway-system -o jsonpath='{.spec.listeners[*].name}' | tr ' ' '\n'
```

**Note:** When using internal services (accessed via Kubernetes DNS only), you typically don't need external routing at all. Consider whether a NebariApp resource is necessary for purely internal services.

### Operator Logs

View operator logs for detailed troubleshooting:
```bash
kubectl logs -n nebari-operator-system -l control-plane=controller-manager -f
```
