# Routing Schema Test Coverage

This document describes the comprehensive test coverage for NebariApp routing schema variations.

## Important Note: Gateway API Default Behavior

When the NebariApp does not specify any `routes` array, the operator creates an HTTPRoute with an **empty matches
array**. However, the Gateway API implementation (Envoy Gateway) automatically adds a default path match of `"/"` with
type `PathPrefix` when the matches array is empty or null.

**This means:**
- The operator creates: `matches: []` (empty)
- Gateway API adds: `matches: [{"path": {"type": "PathPrefix", "value": "/"}}]` (default)

This is **expected Gateway API behavior**, not a bug. Tests verify this by checking for the `"/"` path in the resulting
HTTPRoute.

## Test Files

- **connectivity_test.go**: Basic HTTP/HTTPS connectivity tests
- **nebariapp_test.go**: Basic NebariApp reconciliation tests
- **routing_test.go**: Comprehensive routing schema variation tests (NEW)

## Test Coverage Matrix

### TLS Configuration Tests

| Test Case | TLS Enabled | Expected Listener | File | Status |
|-----------|-------------|-------------------|------|--------|
| Default TLS (enabled) | `true` | `https` | routing_test.go | ✅ |
| TLS Disabled | `false` | `http` | routing_test.go | ✅ |
| No TLS specified (defaults to enabled) | nil | `https` | nebariapp_test.go | ✅ |

### Path-Based Routing Tests

| Test Case | Path Rules | Path Type | File | Status |
|-----------|------------|-----------|------|--------|
| Single PathPrefix | `/api` | `PathPrefix` | routing_test.go | ✅ |
| Single Exact Match | `/health` | `Exact` | routing_test.go | ✅ |
| Multiple Mixed Paths | `/api/v1`, `/api/v2`, `/health` | `PathPrefix`, `PathPrefix`, `Exact` | routing_test.go | ✅ |
| Root Path | `/` | `PathPrefix` | routing_test.go | ✅ |
| No Path Rules | - | Gateway API default ("/") | routing_test.go | ✅ |

### Combined TLS + Path Routing Tests

| Test Case | TLS | Paths | Expected Behavior | File | Status |
|-----------|-----|-------|-------------------|------|--------|
| HTTPS + Multiple Paths | `true` | `/api`, `/app` | HTTPRoute with `https` listener and path rules | routing_test.go | ✅ |
| HTTP + Multiple Paths | `false` | `/api/v1`, `/api/v2`, `/status` | HTTPRoute with `http` listener and path rules | routing_test.go | ✅ |
| HTTPS + No Paths | `true` | - | HTTPRoute with `https` listener, Gateway API adds "/" default | routing_test.go | ✅ |
| HTTP + No Paths | `false` | - | HTTPRoute with `http` listener, Gateway API adds "/" default | connectivity_test.go | ✅ |

### Routing Configuration Presence Tests

| Test Case | Routing Config | Expected Behavior | File | Status |
|-----------|----------------|-------------------|------|--------|
| No Routing Section | `nil` | RoutingReady=False, no HTTPRoute created | routing_test.go | ✅ |
| Only TLS Config | `routing.tls.enabled=true` | HTTPRoute created with HTTPS, Gateway API adds "/" default | routing_test.go | ✅ |
| Only Path Rules | `routing.routes=[...]` | HTTPRoute created with default TLS (HTTPS) | nebariapp_test.go | ⚠️ Implicit |

### Hostname Variations Tests

| Test Case | Hostname Format | File | Status |
|-----------|-----------------|------|--------|
| Simple subdomain | `app.nebari.local` | routing_test.go | ✅ |
| Multi-level subdomain | `app.sub.nebari.local` | routing_test.go | ✅ |
| Hyphenated name | `my-app.nebari.local` | routing_test.go | ✅ |

### Connectivity Tests (HTTP/HTTPS)

| Test Case | TLS | Protocol | Expected Result | File | Status |
|-----------|-----|----------|-----------------|------|--------|
| HTTP Connectivity | `false` | HTTP | 200 OK via Gateway IP | connectivity_test.go | ✅ |
| HTTPS Connectivity | `true` | HTTPS | 200 OK with TLS via Gateway IP | connectivity_test.go | ✅ |

## Test Scenarios Summary

### Total Test Cases: ~20

#### By Category:
- **TLS Configuration**: 3 tests
- **Path-Based Routing**: 5 tests
- **Combined TLS + Paths**: 4 tests
- **Routing Config Presence**: 3 tests
- **Hostname Variations**: 3 tests
- **HTTP/HTTPS Connectivity**: 2 tests

### Coverage by Schema Field:

#### `spec.hostname`
- ✅ Simple subdomain format
- ✅ Multi-level subdomain format
- ✅ Hyphenated names
- ✅ Hostname propagation to HTTPRoute

#### `spec.service`
- ✅ Service name reference
- ✅ Service port configuration
- ✅ Backend reference in HTTPRoute

#### `spec.routing`
- ✅ Missing routing section (nil)
- ✅ Empty routing section
- ✅ TLS-only configuration
- ✅ Routes-only configuration
- ✅ Combined TLS + Routes

#### `spec.routing.tls.enabled`
- ✅ `true` (HTTPS listener)
- ✅ `false` (HTTP listener)
- ✅ `nil` (defaults to HTTPS)

#### `spec.routing.routes[]`
- ✅ Empty/nil (matches all paths)
- ✅ Single route rule
- ✅ Multiple route rules
- ✅ PathPrefix type
- ✅ Exact type
- ✅ Mixed types in single NebariApp
- ✅ Root path `/`
- ✅ Nested paths `/api/v1`, `/api/v2`

#### `spec.routing.routes[].pathType`
- ✅ `PathPrefix`
- ✅ `Exact`
- ✅ Default behavior (PathPrefix)

## Edge Cases Covered

1. **No Routing Configuration**: Verifies that RoutingReady condition is False and no HTTPRoute is created
2. **Root Path Matching**: Tests that `/` path prefix works correctly
3. **Multiple Path Rules**: Ensures all path rules are translated to HTTPRoute matches
4. **Mixed Path Types**: Validates that PathPrefix and Exact can coexist in same NebariApp
5. **TLS + Path Combinations**: Tests all permutations of TLS settings with path routing

## Running the Tests

### Run All Routing Tests

```bash
# With existing cluster
make test-e2e USE_EXISTING_CLUSTER=true

# Create new cluster for tests
make test-e2e
```

### Run Specific Test Suite

```bash
# Run only routing schema tests
ginkgo -tags=e2e --focus="Routing Schema Variations" test/e2e/

# Run only TLS configuration tests
ginkgo -tags=e2e --focus="TLS Configuration" test/e2e/

# Run only path-based routing tests
ginkgo -tags=e2e --focus="Path-Based Routing" test/e2e/
```

## Future Test Additions

Potential additional test scenarios:

1. **Gateway Field Variations**: Test `spec.gateway` = "public" vs "internal" (requires multi-gateway setup)
2. **Auth Configuration**: Test `spec.auth` once authentication reconciler is implemented
3. **Invalid Configurations**: Test validation and error handling for invalid schemas
4. **Update Scenarios**: Test updating routing configuration on existing NebariApp
5. **Conflict Scenarios**: Test multiple NebariApps with same hostname
6. **Performance Tests**: Test large numbers of path rules

## Notes

- All tests use unique hostnames (timestamped) to avoid conflicts
- Tests clean up resources after execution
- Tests use the Gateway IP directly with Host headers to avoid DNS requirements
- TLS tests accept self-signed certificates (`-k` flag in curl)
