# Release Checklist

This checklist guides you through creating a new release of the nebari-operator.

## Prerequisites

- [ ] Maintainer access to the repository
- [ ] Local clone with latest `main` branch
- [ ] `kubebuilder` installed (for Helm chart generation)
- [ ] `helm` installed (for chart packaging)
- [ ] Clean working directory (`git status` shows no changes)

## Pre-Release Validation

- [ ] All PRs for the release are merged to `main`
- [ ] CI is passing on `main` branch
- [ ] E2E tests pass: `USE_EXISTING_CLUSTER=true make test-e2e`
- [ ] Version bump completed (if needed for docs/examples)
- [ ] CHANGELOG.md updated with release notes

## Creating the Release

### 1. Create and checkout the release tag

```bash
git checkout main
git pull origin main
git tag v1.2.3  # Use semantic versioning
git checkout v1.2.3
```

### 2. Generate release manifests

```bash
make prepare-release
```

This command will:
- ✅ Verify you're on a release tag
- ✅ Generate CRDs and deepcopy code
- ✅ Build installer manifests with correct image tag
- ✅ Generate Helm chart
- ✅ Update Helm chart versions
- ✅ Copy `install.yaml` to `deploy/`
- ✅ Stage files for commit

### 3. Review the generated files

```bash
git status
git diff --cached
```

Check that:
- `config/crd/bases/` has the latest CRDs
- `deploy/install.yaml` uses the correct image tag
- `dist/chart/Chart.yaml` has matching versions

### 4. Commit the manifests

```bash
git commit -m "chore: prepare manifests for v1.2.3"
```

### 5. Push the tag

```bash
git push origin v1.2.3
```

### 6. Wait for CI to complete

Monitor the GitHub Actions workflow:
- Go to: https://github.com/nebari-dev/nebari-operator/actions
- Wait for the release workflow to complete
- Verify:
  - [ ] Container images published to `quay.io/nebari/nebari-operator:v1.2.3`
  - [ ] Helm chart packaged and attached to draft release
  - [ ] `install.yaml` attached to draft release

### 7. Publish the GitHub Release

1. Go to: https://github.com/nebari-dev/nebari-operator/releases
2. Find the draft release for `v1.2.3`
3. Review the auto-generated release notes
4. Edit as needed (highlight breaking changes, new features, etc.)
5. Click **"Publish release"**

### 8. Verify helm-repository sync

After publishing the release:
- [ ] Check for automated PR in [nebari-dev/helm-repository](https://github.com/nebari-dev/helm-repository/pulls)
- [ ] Review and merge the PR (this publishes to OCI registry and GitHub Pages)

## Post-Release

### Verify the release

Test installation from released artifacts:

```bash
# Test kubectl installation
kubectl apply -f https://raw.githubusercontent.com/nebari-dev/nebari-operator/v1.2.3/deploy/install.yaml

# Test Helm installation (after helm-repository PR is merged)
helm repo add nebari https://nebari-dev.github.io/helm-repository
helm repo update
helm install nebari-operator nebari/nebari-operator --version 1.2.3
```

### Update infrastructure (optional)

If you maintain infrastructure using this operator:
- Update `nebari-infrastructure-core` manifests/configs to use new version
- Test in dev/staging environments
- Rollout to production when ready

### Announce the release (optional)

- Post in team channels
- Update documentation
- Share in community forums

## Troubleshooting

### "Not on a release tag" error

```bash
# Make sure you're on the tag, not the branch
git checkout v1.2.3
```

### CI workflow fails

- Check GitHub Actions logs
- Fix issues and push a new patch version
- Do not force-push tags; create a new tag instead

### Helm repository sync fails

- Check if `NEBARI_BOT_TOKEN` secret is configured
- Manually create a PR in helm-repository if needed
- See [helm-repository docs](https://github.com/nebari-dev/helm-repository/blob/main/CONTRIBUTING.md)

### Need to update manifests after tag is pushed

If you made a mistake:

```bash
# Delete the remote tag
git push origin :refs/tags/v1.2.3

# Delete the local tag
git tag -d v1.2.3

# Fix issues, recreate tag, and try again
git tag v1.2.3
git checkout v1.2.3
make prepare-release
# ... continue from step 3
```

## Reference

- [Release Process Documentation](./release-process.md)
- [Makefile Reference](../makefile-reference.md)
- [Semantic Versioning](https://semver.org/)
