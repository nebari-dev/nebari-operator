
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
