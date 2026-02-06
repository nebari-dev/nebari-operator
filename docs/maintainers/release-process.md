# Release Process

This document describes the automated release process for the NIC Operator.

## Overview

The release process is fully automated via GitHub Actions and is triggered when you create a GitHub Release. The
workflow will:

1. Run all tests to ensure code quality
2. Generate and update CRDs and RBAC manifests
3. Build and push multi-architecture Docker images to Quay.io
4. Build Go binaries for multiple platforms using GoReleaser
5. Package and publish the Helm chart
6. Generate a consolidated `install.yaml` with all Kubernetes manifests
7. Attach all artifacts to the GitHub Release

## Prerequisites

Before creating a release, ensure you have:

1. **Quay.io Credentials**: Add the following secrets to your GitHub repository:
   - `QUAY_USERNAME`: Your Quay.io username
   - `QUAY_PASSWORD`: Your Quay.io password or robot token

   To add secrets:
   ```
   Settings → Secrets and variables → Actions → New repository secret
   ```

2. **Completed Development Work**:
   - All tests pass locally (`make test`)
   - Linter passes (`make lint`)
   - Manifests are up to date (`make manifests`)
   - Documentation is updated

3. **Version Tagging Strategy**: Follow semantic versioning (e.g., `v1.0.0`, `v1.2.3`)

## Creating a Release

### Step 1: Prepare the Release

1. **Update documentation** if needed (especially if new configuration options were added)

2. **Update any version references** in code or documentation

3. **Commit and push** all changes:
   ```bash
   git add .
   git commit -m "chore: prepare for vX.Y.Z release"
   git push origin main
   ```

### Step 2: Create a Git Tag

Create and push a version tag:

```bash
# Create a tag (e.g., v1.0.0)
git tag -a v1.0.0 -m "Release v1.0.0"

# Push the tag
git push origin v1.0.0
```

### Step 3: Create GitHub Release

There are two ways to create a GitHub Release:

#### Option A: Via GitHub Web UI (Recommended)

1. Go to your repository on GitHub
2. Click on "Releases" (right sidebar)
3. Click "Draft a new release"
4. Select the tag you just created (e.g., `v1.0.0`)
5. Set the release title (e.g., "v1.0.0")
6. Write release notes describing:
   - New features
   - Bug fixes
   - Breaking changes
   - Upgrade instructions
7. Click "Publish release"

#### Option B: Via GitHub CLI

```bash
gh release create v1.0.0 \
  --title "v1.0.0" \
  --notes "Release notes here..."
```

### Step 4: Monitor the Release Workflow

1. Go to the "Actions" tab in your GitHub repository
2. Find the "Release" workflow run
3. Monitor the progress of all jobs:
   - **tests**: Runs unit tests and linter
   - **build-manifests**: Generates the install.yaml
   - **docker-build-push**: Builds and pushes Docker images
   - **publish-helm-chart**: Packages and publishes the Helm chart
   - **goreleaser**: Builds Go binaries for multiple platforms
   - **upload-manifests**: Attaches manifests to the release

The workflow typically takes 5-10 minutes to complete.

## Release Artifacts

After the workflow completes successfully, the following artifacts will be available:

### 1. Docker Images (Quay.io)

Multi-architecture images will be pushed to:
```
quay.io/nebari/nebari-operator:<version>
quay.io/nebari/nebari-operator:latest
```

Supported architectures:
- `linux/amd64`
- `linux/arm64`

### 2. Go Binaries (GitHub Release)

Platform-specific binaries will be attached to the GitHub Release:
- `nebari-operator_<version>_Linux_x86_64.tar.gz`
- `nebari-operator_<version>_Linux_arm64.tar.gz`
- `nebari-operator_<version>_Darwin_x86_64.tar.gz`
- `nebari-operator_<version>_Darwin_arm64.tar.gz`
- `nebari-operator_<version>_Windows_x86_64.zip`

### 3. Kubernetes Manifests (GitHub Release)

A consolidated installation file:
- `install.yaml` - Contains all CRDs, RBAC, and deployment manifests Helm Chart (GitHub Release)

The Helm chart package:
- `nic-operator-<version>.tgz` - Helm chart for deploying the operator

### 5.
### 4. Checksums
Using Helm (Recommended)

Install the operator using the Helm chart:

```bash
# Add the release as a Helm repository (download the chart first)
curl -LO https://github.com/nebari-dev/nic-operator/releases/download/v1.0.0/nic-operator-1.0.0.tgz

# Install the chart
helm install nebari-operator nic-operator-1.0.0.tgz \
  --create-namespace \
  --namespace nebari-operator-system
```

Or install directly from URL:

```bash
helm install nebari-operator \
  https://github.com/nebari-dev/nic-operator/releases/download/v1.0.0/nic-operator-1.0.0.tgz \
  --create-namespace \
  --namespace nebari-operator-system
```

### Using kubectl

Install the operator using kubectl:

```bash
kubectl apply -f https://github.com/nebari-dev/nic

Install the operator using kubectl:

```bash
kubectl apply -f https://github.com/nebari-dev/nebari-operator/releases/download/v1.0.0/install.yaml
```

### For Local Development

Download and run the binary for your platform:

```bash
# Linux (amd64)
curl -LO https://github.com/nebari-dev/nebari-operator/releases/download/v1.0.0/nebari-operator_1.0.0_Linux_x86_64.tar.gz
tar -xzf nebari-operator_1.0.0_Linux_x86_64.tar.gz
./manager

# macOS (arm64)
curl -LO https://github.com/nebari-dev/nebari-operator/releases/download/v1.0.0/nebari-operator_1.0.0_Darwin_arm64.tar.gz
tar -xzf nebari-operator_1.0.0_Darwin_arm64.tar.gz
./manager
```

## Version Numbering

Follow [Semantic Versioning](https://semver.org/):

- **MAJOR** version (`vX.0.0`): Incompatible API changes
- **MINOR** version (`v1.X.0`): Backward-compatible functionality additions
- **PATCH** version (`v1.0.X`): Backward-compatible bug fixes

### Examples:

- `v1.0.0` - Initial stable release
- `v1.1.0` - Added new features, backward compatible
- `v1.1.1` - Bug fixes, no new features
- `v2.0.0` - Breaking changes, requires migration

### Pre-releases:

You can also create pre-releases for testing:
- `v1.0.0-alpha.1` - Alpha release
- `v1.0.0-beta.1` - Beta release
- `v1.0.0-rc.1` - Release candidate

To mark a release as a pre-release in GitHub, check the "This is a pre-release" checkbox.

## API Versioning

When introducing breaking changes to the CRD API:

1. Create a new API version (e.g., `v2`)
2. Maintain the old version for backward compatibility
3. Provide migration documentation
4. Announce deprecation of the old version

Example structure:
```
api/
  v1/        # Existing stable API
  v2/        # New API version
```

## Troubleshooting

### Release Workflow Fails

1. **Tests fail**: Fix the failing tests and create a new release
2. **Docker push fails**: Check that `QUAY_USERNAME` and `QUAY_PASSWORD` secrets are set correctly
3. **GoReleaser fails**: Check the GoReleaser configuration in `.goreleaser.yml`

### Quay.io Authentication Issues

If the Docker push fails with authentication errors:

1. Verify secrets are set in GitHub:
   ```
   Settings → Secrets and variables → Actions
   ```

2. For Quay.io robot accounts:
   - Go to Quay.io → Account Settings → Robot Accounts
   - Create a robot account with write permissions to the repository
   - Use the robot account credentials as secrets

3. Test locally:
   ```bash
   echo $QUAY_PASSWORD | docker login quay.io -u $QUAY_USERNAME --password-stdin
   docker pull quay.io/nebari/nebari-operator:latest
   ```

### Missing Artifacts

If artifacts are missing from the release:

1. Check the workflow logs in the Actions tab
2. Ensure all jobs completed successfully
3. Re-run failed jobs if needed

### Rollback a Release

If you need to rollback a release:

1. Delete the GitHub Release (optional)
2. Delete the tag:
   ```bash
   git tag -d v1.0.0
   git push origin :refs/tags/v1.0.0
   ```
3. Revert problematic commits
4. Create a new release with a patch version

## Manual Release (Emergency)

If the automated workflow fails and you need to release manually: Package Helm Chart

```bash
export VERSION=1.0.0  # Note: no 'v' prefix for Helm chart version

# Update Chart.yaml versions
### 5. Upload to GitHub

Manually upload the files to the GitHub Release:
- Binaries from `dist/`
- `dist/install.yaml`
- `dist/nic-operator-<version>.tgz`rt --destination dist/
```

### 4.
### 1. Build and Push Docker Image

```bash
export VERSION=v1.0.0
export IMG=quay.io/nebari/nebari-operator:${VERSION}

# Login to Quay.io
docker login quay.io

# Build and push
make docker-buildx IMG=${IMG}

# Also tag as latest
docker tag ${IMG} quay.io/nebari/nebari-operator:latest
docker push quay.io/nebari/nebari-operator:latest
```

### 2. Generate Manifests

```bash
export VERSION=v1.0.0
export IMG=quay.io/nebari/nebari-operator:${VERSION}
make build-installer IMG=${IMG}
```

**Note**: The `IMG` parameter is required to update image references in the generated manifests.

### 3. Build Binaries

```bash
goreleaser release --clean
```

### 4. Upload to GitHub

Manually upload the files to the GitHub Release:
- Binaries from `dist/`
- `dist/install.yaml`
- `checksums.txt`

## Best Practices

1. **Test Before Release**: Always run tests locally before creating a release
2. **Write Good Release Notes**: Clearly document what changed
3. **Follow Semantic Versioning**: Be consistent with version numbers
4. **Announce Breaking Changes**: Clearly communicate incompatible changes
5. **Maintain Changelog**: Keep CHANGELOG.md updated
6. **Tag Appropriately**: Use annotated tags with descriptions
7. **Monitor Releases**: Watch the workflow execution
8. **Validate Release**: Test the released artifacts before announcing

## Scheduled Releases

Consider establishing a release schedule:

- **Major releases**: Once or twice a year
- **Minor releases**: Every 1-3 months
- **Patch releases**: As needed for critical bugs

## Communication

After a successful release:

1. Update project documentation
2. Announce on relevant channels (Slack, mailing list, etc.)
3. Update examples and tutorials
4. Notify users of breaking changes
5. Celebrate! 🎉

## Additional Resources

- [GitHub Actions Documentation](https://docs.github.com/en/actions)
- [GoReleaser Documentation](https://goreleaser.com/)
- [Semantic Versioning](https://semver.org/)
- [Kubebuilder Book](https://book.kubebuilder.io/)
- [Quay.io Documentation](https://docs.quay.io/)
