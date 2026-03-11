# Contributing to Nebari Operator

Thank you for your interest in contributing! This guide will help you get started with development.

## Development Workflow

### Prerequisites

- Go 1.23 or later
- Docker or Podman (for building images)
- Kubernetes cluster (kind, k3d, minikube, or cloud provider)
- `kubectl` configured to access your cluster
- `kubebuilder` (for Helm chart generation)

### Setup

1. **Clone the repository**
   ```bash
   git clone https://github.com/nebari-dev/nebari-operator.git
   cd nebari-operator
   ```

2. **Install dependencies**
   ```bash
   go mod download
   make controller-gen   # Install code generation tools
   ```

3. **Verify your setup**
   ```bash
   make test  # Run unit tests
   ```

## Making Changes

### Modifying the API

When you modify the NebariApp CRD (in `api/v1/nebariapp_types.go`):

1. **Make your changes** to the Go types
2. **Regenerate CRDs and code**:
   ```bash
   make generate-dev
   ```
   This will:
   - Generate DeepCopy methods
   - Update CRD manifests in `config/crd/bases/`

3. **Commit both source and generated files**:
   ```bash
   git add api/ config/crd/bases/
   git commit -m "feat: add new field to NebariApp"
   ```

**Important**: Always run `make generate-dev` after API changes. CI will fail if generated files are out of sync.

### Modifying the Controller

When you modify controller logic (in `internal/controller/`):

1. **Make your changes**
2. **Run tests**:
   ```bash
   make test-unit  # Unit tests
   make test-e2e   # End-to-end tests (requires cluster)
   ```

3. **Commit**:
   ```bash
   git add internal/controller/
   git commit -m "fix: improve reconciliation logic"
   ```

### Testing Locally

#### Run the operator in-cluster

```bash
# Build and deploy
make docker-build IMG=nebari-operator:dev
make deploy IMG=nebari-operator:dev

# Check logs
kubectl logs -f deployment/nebari-operator-controller-manager \
  -n nebari-operator-system \
  -c manager
```

#### Run the operator locally

```bash
# Install CRDs
make install

# Run controller (watches cluster)
make run
```

### Useful Make Targets

| Command | Description |
|---------|-------------|
| `make generate-dev` | Generate CRDs and DeepCopy code (run after API changes) |
| `make generate-all` | Generate CRDs, manifests, and Helm chart (comprehensive) |
| `make test` | Run all unit tests |
| `make test-unit` | Run controller unit tests with coverage |
| `make test-e2e` | Run end-to-end tests |
| `make build` | Build the operator binary |
| `make docker-build` | Build Docker image |
| `make deploy` | Deploy to cluster |
| `make lint` | Run linter |
| `make help` | Show all available targets |

## Pull Request Process

1. **Create a branch**:
   ```bash
   git checkout -b feat/my-new-feature
   ```

2. **Make changes and test**:
   ```bash
   # Make your changes
   make generate-dev  # If you modified the API
   make test          # Run tests
   make lint          # Check code quality
   ```

3. **Commit with clear messages**:
   ```bash
   git commit -m "feat: add support for custom domains"
   ```
   Use [conventional commits](https://www.conventionalcommits.org/):
   - `feat:` - New features
   - `fix:` - Bug fixes
   - `docs:` - Documentation changes
   - `chore:` - Maintenance tasks
   - `test:` - Test updates

4. **Push and create PR**:
   ```bash
   git push origin feat/my-new-feature
   ```
   Then open a PR on GitHub.

5. **CI Checks**: Your PR must pass:
   - ✅ Unit tests
   - ✅ E2E tests
   - ✅ Linter checks
   - ✅ Generated files are up-to-date

## Common Tasks

### Adding a New Field to NebariApp

1. Edit `api/v1/nebariapp_types.go`
2. Add kubebuilder markers for validation
3. Run `make generate-dev`
4. Update controller logic in `internal/controller/`
5. Add tests
6. Document in `docs/configuration-reference.md`

### Debugging

**Enable verbose logging**:
```bash
kubectl set env deployment/nebari-operator-controller-manager \
  LOGLEVEL=debug \
  -n nebari-operator-system
```

**Check resource events**:
```bash
kubectl describe nebariapp my-app
kubectl get events --sort-by='.lastTimestamp'
```

**Check controller logs**:
```bash
kubectl logs -f deployment/nebari-operator-controller-manager \
  -n nebari-operator-system \
  -c manager
```

## Getting Help

- 📖 [Documentation](https://github.com/nebari-dev/nebari-operator/tree/main/docs)
- 💬 [GitHub Discussions](https://github.com/nebari-dev/nebari-operator/discussions)
- 🐛 [Issue Tracker](https://github.com/nebari-dev/nebari-operator/issues)

## Code of Conduct

Please note that this project adheres to the Contributor Covenant Code of Conduct. By participating, you are expected to uphold this code.

## License

By contributing, you agree that your contributions will be licensed under the Apache License 2.0.
