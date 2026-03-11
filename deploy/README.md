# Deployment Manifests

This directory contains release-ready Kubernetes manifests for the nebari-operator.

## Files

### `install.yaml`

A consolidated YAML file containing all resources needed to deploy the operator:
- CustomResourceDefinitions (CRDs)
- RBAC roles and bindings
- Operator deployment
- Service accounts
- Webhooks (if applicable)

This file is generated and committed **only on release tags** using `make prepare-release`.

## Usage

### Install the operator

```bash
kubectl apply -f https://raw.githubusercontent.com/nebari-dev/nebari-operator/v1.0.0/deploy/install.yaml
```

Or from a local clone:

```bash
kubectl apply -f deploy/install.yaml
```

### Uninstall the operator

```bash
kubectl delete -f deploy/install.yaml
```

## For Developers

**Do not manually edit files in this directory.**

During development, this directory is gitignored. Files are only committed during the release process:

1. Create and checkout a release tag
2. Run `make prepare-release`
3. Review and commit the generated manifests
4. Push the tag

See [docs/maintainers/release-checklist.md](../docs/maintainers/release-checklist.md) for the complete process.

## Alternative Installation Methods

- **Helm Chart**: Available via [nebari-dev/helm-repository](https://github.com/nebari-dev/helm-repository)
- **OCI Registry**: `helm install nebari-operator oci://quay.io/nebari-dev/nebari-operator --version 1.0.0`
- **Kustomize**: Use `config/` directory as a base
