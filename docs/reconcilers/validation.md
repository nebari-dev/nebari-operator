# Validation Reconciler

> **Part of:** [Reconciler Architecture](README.md) **Phase:** 1 of 3 (Validation → Routing → Authentication)
> **Purpose:** Ensure all prerequisites are met before resource provisioning

## Overview

The core validation structure is the first stage of the NebariApp reconciliation process. It ensures that all
prerequisites and dependencies are met before proceeding with resource provisioning (routing, TLS, authentication).

The validation logic is encapsulated in the `CoreReconciler` located at `internal/controller/reconcilers/core/`.

## Architecture

```
NebariAppReconciler
  └─> CoreReconciler.ValidateSpec()
       ├─> ValidateNamespaceOptIn()
       └─> ValidateService()
```

### CoreReconciler

The `CoreReconciler` is responsible for:
- Validating namespace opt-in requirements
- Validating service references
- Recording Kubernetes events for validation outcomes
- Setting appropriate status conditions

**Fields:**
- `Client`: Kubernetes client for API interactions
- `Scheme`: Runtime scheme for object type registration
- `Recorder`: Event recorder for emitting Kubernetes events

## Validation Steps

### 1. Namespace Opt-In Validation

**Purpose**: Ensures that only explicitly managed namespaces can host NebariApp resources.

**Validation Logic**:
```go
func ValidateNamespaceOptIn(ctx context.Context, c client.Client, nebariApp *appsv1.NebariApp) error
```

**Requirements**:
- The namespace must exist
- The namespace must have the label: `nebari.dev/managed=true`

**On Failure**:
- Event: `Warning` with reason `NamespaceNotOptedIn`
- Condition: `Ready=False` with reason `NamespaceNotOptedIn`
- Error message: "namespace {name} is not opted-in to Nebari management"

**Example**:
```yaml
apiVersion: v1
kind: Namespace
metadata:
  name: my-app-namespace
  labels:
    nebari.dev/managed: "true"
```

### 2. Service Reference Validation

**Purpose**: Ensures that the backend service specified in the NebariApp exists and exposes the configured port.

**Validation Logic**:
```go
func ValidateService(ctx context.Context, c client.Client, nebariApp *appsv1.NebariApp) error
```

**Requirements**:
- The service must exist in the same namespace as the NebariApp
- The service must expose the port specified in `spec.service.port`

**On Failure**:
- Event: `Warning` with reason `ServiceNotFound`
- Condition: `Ready=False` with reason `ServiceNotFound`
- Error message: "service {name} not found" or "service {name} does not expose port {port}"

**Example**:
```yaml
apiVersion: reconcilers.nebari.dev/v1
kind: NebariApp
metadata:
  name: my-app
  namespace: my-app-namespace
spec:
  hostname: myapp.example.com
  service:
    name: my-backend-service  # Must exist
    port: 8080               # Must be exposed by the service
```

## Status Management

### Conditions

The validation process sets the `Ready` condition on the NebariApp status:

**During Validation**:
```yaml
status:
  conditions:
  - type: Ready
    status: Unknown
    reason: Reconciling
    message: "Reconciliation in progress"
```

**On Validation Failure**:
```yaml
status:
  conditions:
  - type: Ready
    status: "False"
    reason: NamespaceNotOptedIn  # or ServiceNotFound
    message: "namespace xyz is not opted-in to Nebari management"
```

**On Validation Success**:
```yaml
status:
  conditions:
  - type: Ready
    status: "True"
    reason: ReconcileSuccess
    message: "NebariApp reconciled successfully"
```

### Events

Validation outcomes are recorded as Kubernetes events:

**Success**:
```
Type    Reason             Age   Message
Normal  ValidationSuccess  10s   NebariApp validation completed successfully
```

**Failure**:
```
Type     Reason                 Age   Message
Warning  NamespaceNotOptedIn    10s   namespace xyz is not opted-in...
Warning  ServiceNotFound        10s   service my-service not found
```

## Integration with Controller

The `NebariAppReconciler` calls the core validation during its reconciliation loop:

```go
func (r *NebariAppReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
    // ... fetch NebariApp ...

    // Initialize status with Reconciling condition
    conditions.SetCondition(nebariApp, appsv1.ConditionTypeReady, metav1.ConditionUnknown,
        appsv1.ReasonReconciling, "Reconciliation in progress")

    // Run core validation
    if err := r.CoreReconciler.ValidateSpec(ctx, nebariApp); err != nil {
        // Update status with failure condition
        r.Status().Update(ctx, nebariApp)
        return ctrl.Result{RequeueAfter: 5 * time.Minute}, nil
    }

    // Set success condition
    conditions.SetCondition(nebariApp, appsv1.ConditionTypeReady, metav1.ConditionTrue,
        appsv1.ReasonReconcileSuccess, "NebariApp reconciled successfully")

    // Update status
    r.Status().Update(ctx, nebariApp)

    return ctrl.Result{RequeueAfter: time.Minute}, nil
}
```

## Error Handling

### Requeue Strategy

- **Validation Failure**: Requeue after 5 minutes
  - Gives time for manual intervention (e.g., adding namespace label, creating service)
  - Prevents excessive API calls and log spam

- **Success**: Requeue after 1 minute
  - Periodic reconciliation to detect configuration drift
  - Will be adjusted when implementing full reconciliation logic

### Temporary vs Permanent Failures

Currently, all validation failures are treated as temporary:
- Namespace label can be added
- Service can be created
- Both trigger reconciliation via watches

Future enhancements may distinguish:
- **Temporary**: Service not ready yet, certificate pending
- **Permanent**: Invalid hostname format, misconfigured spec

## Constants and Reasons

All condition reasons and event reasons are defined in `api/v1/nebariapp_types.go` as the single source of truth:

**Condition Reasons**:
- `ReasonReconciling`: Reconciliation in progress
- `ReasonReconcileSuccess`: Successful reconciliation
- `ReasonNamespaceNotOptedIn`: Namespace missing required label
- `ReasonServiceNotFound`: Referenced service doesn't exist

**Event Reasons**:
- `EventReasonValidationSuccess`: Validation passed
- `EventReasonNamespaceNotOptIn`: Namespace not opted-in
- `EventReasonServiceNotFound`: Service not found

## Testing

Unit tests are located in `internal/controller/reconcilers/core/reconciler_test.go`:

- `TestValidateNamespaceOptIn`: Tests namespace validation logic
- `TestValidateService`: Tests service validation logic
- `TestCoreReconciliationValidateSpec`: Integration test for full validation flow

Tests use fake Kubernetes clients and event recorders to verify:
- Correct conditions are set
- Appropriate events are emitted
- Error messages are descriptive

## Future Enhancements

Planned additions to core validation:

1. **Gateway Validation**: Verify target gateway exists
2. **TLS Secret Validation**: Check for custom TLS secrets if specified
3. **Port Accessibility**: Verify service port is accessible (health checks)
4. **Hostname Uniqueness**: Prevent duplicate hostnames across NebariApps
5. **Resource Quotas**: Validate namespace has sufficient quota for ingress resources

## Related Documentation

- [NebariApp API Specification](../api/v1/nebariapp_types.go)
- [Controller Implementation](../internal/controller/nebariapp_controller.go)
- [Conditions Utilities](../internal/controller/utils/conditions/)
