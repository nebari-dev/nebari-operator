
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

### Operator Logs

View operator logs for detailed troubleshooting:
```bash
kubectl logs -n nebari-operator-system -l control-plane=controller-manager -f
```
