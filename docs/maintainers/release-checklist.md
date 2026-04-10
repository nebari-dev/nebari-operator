# Release Checklist

This checklist guides you through creating a new release of the nebari-operator.

> **Note**: The release process is fully automated via GitHub Actions. You only need to create and push a tag, then publish the GitHub release. All artifacts (Docker images, Helm charts, install.yaml) are generated automatically by CI.

## Prerequisites

- [ ] Maintainer access to the repository
- [ ] GitHub CLI (`gh`) installed (optional, but recommended)
- [ ] Local clone with latest `main` branch

## Pre-Release Validation

- [ ] All PRs for the release are merged to `main`
- [ ] CI is passing on `main` branch
- [ ] E2E tests pass locally (optional): `USE_EXISTING_CLUSTER=true make test-e2e`
- [ ] Documentation is updated as needed
- [ ] Review closed issues/PRs since last release for release notes

## Creating the Release

### 1. Ensure you're on the latest main

```bash
git checkout main
git pull origin main
```

### 2. Create and push the release tag

```bash
# Create a tag (use semantic versioning)
git tag -a v1.2.3 -m "Release v1.2.3"

# Push the tag to trigger the release workflow
git push origin v1.2.3
```

### 3. Create the GitHub Release

The release workflow is triggered by publishing a GitHub release — it does **not** trigger on tag push alone.

#### Option A: Via GitHub CLI (Recommended)

```bash
gh release create v1.2.3 \
  --title "v1.2.3" \
  --generate-notes
```

The `--generate-notes` flag automatically creates release notes from PR titles.

#### Option B: Via GitHub Web UI

1. Go to [Releases](https://github.com/nebari-dev/nebari-operator/releases)
2. Click "Draft a new release"
3. Select the tag `v1.2.3`
4. Set title to "v1.2.3"
5. Click "Generate release notes" button
6. Review and edit the notes as needed:
   - Highlight breaking changes
   - Add upgrade instructions if needed
   - Organize by feature/bugfix/docs
7. Click "Publish release"

### 4. Monitor the Release Workflow

Go to the [Actions tab](https://github.com/nebari-dev/nebari-operator/actions) and monitor the Release workflow:

Wait for all jobs to complete (typically 5-10 minutes):
- [ ] **tests**: Unit tests and linter pass
- [ ] **build-manifests**: Generate and upload install.yaml
- [ ] **docker-build-push**: Build multi-arch images
- [ ] **merge-manifest**: Create multi-arch manifest
- [ ] **goreleaser**: Build Go binaries
- [ ] **publish-helm-chart**: Package and upload Helm chart
- [ ] **sync-helm-repository**: Sync to helm-repository (if enabled)

### 5. Verify the Release Artifacts

Check that all artifacts are attached to the release:
- [ ] `install.yaml` - Kubernetes manifests for kubectl installation
- [ ] `nebari-operator-<version>.tgz` - Helm chart package
- [ ] `nebari-operator_<version>_<os>_<arch>.tar.gz` - Go binaries for each platform
- [ ] `checksums.txt` - SHA256 checksums

### 6. Verify Docker Images

Check that multi-arch images are published:
- [ ] Visit [quay.io/nebari/nebari-operator](https://quay.io/repository/nebari/nebari-operator?tab=tags)
- [ ] Verify tag `v1.2.3` exists with both `linux/amd64` and `linux/arm64`
- [ ] Verify `latest` tag is updated

### 7. Verify Helm Repository Sync (if enabled)

After publishing the release:
- [ ] Check for automated PR in [nebari-dev/helm-repository](https://github.com/nebari-dev/helm-repository/pulls)
- [ ] Review and merge the PR (this publishes to OCI registry and GitHub Pages)

## Post-Release Verification

### Test kubectl Installation

```bash
kubectl apply -f https://github.com/nebari-dev/nebari-operator/releases/download/v1.2.3/install.yaml
```

### Test Helm Installation

```bash
# Install from OCI registry (after helm-repository sync)
helm install nebari-operator oci://quay.io/nebari/charts/nebari-operator \
  --version 1.2.3 \
  --create-namespace \
  --namespace nebari-operator-system
```

Or install directly from the GitHub release artifact:

```bash
helm install nebari-operator \
  https://github.com/nebari-dev/nebari-operator/releases/download/v1.2.3/nebari-operator-1.2.3.tgz \
  --create-namespace \
  --namespace nebari-operator-system
```

### Announce the Release (Optional)

- Post in team channels
- Update documentation sites
- Share in community forums

## Troubleshooting

### Release Workflow Fails

**Check the logs**: Go to Actions tab and view the failed job logs.

**Common issues**:
- **goreleaser dirty state**: Ensure `dist/` is fully ignored in `.gitignore`
- **Docker build fails**: Check Quay.io credentials (`QUAY_USERNAME`, `QUAY_PASSWORD`)
- **Tests fail**: Fix the issues and create a new tag (e.g., `v1.2.4`)

**Resolution**: Do not force-push tags. If a release fails, create a new patch version.

### Need to Delete a Release

If you need to delete a failed release:

```bash
# Delete the GitHub release
gh release delete v1.2.3 --yes

# Delete the remote tag
git push origin :refs/tags/v1.2.3

# Delete the local tag  
git tag -d v1.2.3

# Fix issues, recreate tag with new version
git tag -a v1.2.4 -m "Release v1.2.4"
git push origin v1.2.4
gh release create v1.2.4 --generate-notes
```

### Helm Repository Sync Fails

- Check if `NEBARI_HELM_REPO_TOKEN` secret is configured in repository settings
- Manually create a PR in [helm-repository](https://github.com/nebari-dev/helm-repository) if needed
- See [helm-repository docs](https://github.com/nebari-dev/helm-repository/blob/main/CONTRIBUTING.md)

## Reference

- [Release Process Documentation](./release-process.md)
- [Makefile Reference](../makefile-reference.md)
- [Semantic Versioning](https://semver.org/)
