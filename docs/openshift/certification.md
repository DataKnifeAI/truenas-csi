# Red Hat OpenShift Certification Guide

This document describes the process for certifying the TrueNAS CSI Driver for Red Hat OpenShift.

## Overview

Red Hat OpenShift certification involves three levels:

1. **Container Certification** - Certify individual container images
2. **Operator Certification** - Certify the operator bundle for OperatorHub
3. **CSI Certification** - Certify the CSI driver functionality (badge)

All three must be completed for full CSI certification.

## Prerequisites

- Red Hat Partner Connect account: https://connect.redhat.com
- OpenShift cluster (4.17+) for testing
- TrueNAS SCALE 25.10.0+ for integration testing
- Tools installed:
  - `preflight` - Container certification tool
  - `operator-sdk` - Operator development and testing
  - `oc` - OpenShift CLI

## Container Images

The following images must be certified:

| Image | Registry | Purpose |
|-------|----------|---------|
| `truenas-csi` | quay.io/truenas_solutions | CSI driver |
| `truenas-csi-operator` | quay.io/truenas_solutions | Operator |
| `truenas-csi-operator-bundle` | quay.io/truenas_solutions | OLM bundle |

### Building UBI-Based Images

All images must use Red Hat Universal Base Image (UBI) for certification:

```bash
# From project root
make build-all    # Build all images (driver, operator, bundle)
```

The driver uses `Dockerfile.ubi` and the operator uses `operator/Dockerfile.ubi`.

### Required Container Labels

Both driver and operator images include these required labels:

- `name` - Container name
- `vendor` - "TrueNAS"
- `version` - Semantic version
- `release` - Release number
- `summary` - Short description
- `description` - Detailed description
- `maintainer` - Contact email

### License File

Both images include `/licenses/LICENSE` (GPLv3) as required by Red Hat certification.

## Container Certification

### Step 1: Create Certification Projects

1. Log in to [Red Hat Partner Connect](https://connect.redhat.com)
2. Navigate to **Product Certification** > **Manage Products**
3. Create a new certification project for each container:
   - TrueNAS CSI Driver
   - TrueNAS CSI Operator

### Step 2: Configure Driver as Privileged

The CSI driver requires root privileges for mount operations:

1. Go to the driver certification project **Settings** tab
2. Under **Host level access**, select **Privileged**
3. Save changes

This exempts the container from the `RunAsNonRoot` check.

### Step 3: Run Preflight Checks

```bash
# Check driver image
preflight check container quay.io/truenas_solutions/truenas-csi:v0.1.0

# Check operator image
preflight check container quay.io/truenas_solutions/truenas-csi-operator:v0.1.0
```

Expected results:
- Driver: All pass except `RunAsNonRoot` (exempted via Privileged setting)
- Operator: All pass

### Step 4: Submit Results

```bash
# Submit with API token from Partner Connect
preflight check container quay.io/truenas_solutions/truenas-csi:v0.1.0 \
  --submit \
  --pyxis-api-token=<your-token> \
  --certification-project-id=<project-id>
```

## Operator Certification

### Step 1: Validate Bundle

Run the OLM scorecard tests:

```bash
cd operator
operator-sdk scorecard bundle/
```

All 5 tests must pass:
- `olm-bundle-validation`
- `olm-crds-have-validation`
- `olm-crds-have-resources`
- `olm-spec-descriptors`
- `olm-status-descriptors`

### Step 2: Run Tests

```bash
# Unit tests
make test

# E2E tests (creates Kind cluster)
make test-e2e

# Integration tests (requires CRC/OpenShift + TrueNAS)
make test-integration TRUENAS_IP=<ip> TRUENAS_API_KEY=<key>
```

### Step 3: Create Operator Project

1. In Partner Connect, create an **Operator** certification project
2. Link the certified container images
3. Upload the bundle or provide the bundle image reference

### Step 4: Submit for Review

Push the bundle image and submit:

```bash
make bundle-push
```

## CSI Certification (Badge)

CSI certification is a functional badge that validates storage capabilities.

### Prerequisites

Before CSI certification:
1. Container images must be certified and published
2. Operator must be certified and published
3. CSI sidecar images must be certified (or use Red Hat certified versions)

### CSI Capabilities

The driver declares these capabilities in `deploy/openshift/csi-capabilities.yaml`:

**Controller Capabilities:**
- `CREATE_DELETE_VOLUME`
- `CREATE_DELETE_SNAPSHOT`
- `CLONE_VOLUME`
- `EXPAND_VOLUME`
- `LIST_VOLUMES`
- `GET_CAPACITY`

**Node Capabilities:**
- `STAGE_UNSTAGE_VOLUME`
- `GET_VOLUME_STATS`
- `EXPAND_VOLUME`

**Supported Protocols:**
- NFS (ReadWriteMany)
- iSCSI (ReadWriteOnce, block volumes)

**Features:**
- Dynamic provisioning
- Volume expansion
- Volume snapshots
- Volume cloning
- Raw block volumes

### Running CSI Certification Tests

CSI certification requires running the OpenShift End-to-End CSI tests.

#### Option 1: Manual Testing

```bash
# Clone the OpenShift tests
git clone https://github.com/openshift/origin.git
cd origin

# Build the test binary
make build WHAT=cmd/openshift-tests

# Run CSI tests
./openshift-tests run openshift/csi \
  --provider '{"type":"skeleton"}' \
  --junit-dir=/tmp/csi-test-results
```

#### Option 2: DCI (Distributed CI)

Red Hat provides DCI for automated certification testing:

1. Set up DCI agent: https://docs.distributed-ci.io/
2. Configure the CSI manifest file
3. Run certification pipeline

### CSI Test Categories

Tests are run for each protocol (NFS, iSCSI):

| Category | Description |
|----------|-------------|
| Provisioning | Create/delete volumes |
| Snapshots | Create/restore snapshots |
| Cloning | Clone volumes |
| Expansion | Resize volumes |
| Mount | Mount/unmount operations |
| Block | Raw block volume support |

### Test Manifest

Create a manifest file for DCI testing:

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: csi-test-manifest
data:
  driver: csi.truenas.io
  storageClass: truenas-nfs
  snapshotClass: truenas-snapshots
  capabilities:
    - persistence
    - block
    - fsGroup
    - exec
    - snapshotDataSource
    - pvcDataSource
    - expansion
```

## Certification Maintenance

### Version Updates

When releasing a new version:

1. Update version in all Dockerfiles and manifests
2. Rebuild and push images:
   ```bash
   make release VERSION=x.y.z
   ```
3. Re-run preflight checks
4. Submit updated images to Partner Connect

### OpenShift Version Support

- Certifications are specific to OpenShift minor versions
- Recertify for each new OpenShift minor release
- Certifications valid for 12 months or until OpenShift version EOL

### Annual Recertification

Red Hat requires annual recertification:
1. Re-run all certification tests
2. Update documentation for any changes
3. Submit recertification through Partner Connect

## Troubleshooting

### Preflight Failures

| Check | Common Fix |
|-------|------------|
| `RunAsNonRoot` | Set "Privileged" in project settings (CSI driver only) |
| `HasLicense` | Ensure `/licenses/LICENSE` exists in image |
| `HasRequiredLabel` | Add missing labels to Dockerfile |
| `BasedOnUbi` | Use UBI base image |
| `HasNoProhibitedPackages` | Remove RHEL kernel packages |

### Scorecard Failures

| Test | Common Fix |
|------|------------|
| `olm-spec-descriptors` | Add x-descriptors to CRD spec fields |
| `olm-status-descriptors` | Add x-descriptors to CRD status fields |
| `olm-crds-have-resources` | Add resources list to CSV |

### CSI Test Failures

1. Check driver logs: `oc logs -n truenas-csi deploy/truenas-csi-controller`
2. Check node logs: `oc logs -n truenas-csi ds/truenas-csi-node`
3. Verify TrueNAS connectivity
4. Check StorageClass and VolumeSnapshotClass configuration

## References

- [Red Hat Partner Connect](https://connect.redhat.com)
- [Red Hat Software Certification Guide](https://docs.redhat.com/en/documentation/red_hat_software_certification/2025/html/red_hat_software_certification_workflow_guide/)
- [OpenShift CSI Certification Policy](https://docs.redhat.com/en/documentation/red_hat_software_certification/2025/html-single/red_hat_openshift_software_certification_policy_guide/)
- [Preflight Documentation](https://github.com/redhat-openshift-ecosystem/openshift-preflight)
- [Operator SDK Scorecard](https://sdk.operatorframework.io/docs/testing-operators/scorecard/)
- [CSI Specification](https://github.com/container-storage-interface/spec)

## Quick Reference

### Build and Push All Images

```bash
# Build and push everything including 'latest' tag
make release VERSION=0.1.0
```

### Run All Certification Checks

```bash
# Container checks
preflight check container quay.io/truenas_solutions/truenas-csi:v0.1.0
preflight check container quay.io/truenas_solutions/truenas-csi-operator:v0.1.0

# Operator checks
cd operator
make test              # Unit tests
make test-e2e          # E2E tests
operator-sdk scorecard bundle/

# Integration tests
make test-integration TRUENAS_IP=<ip> TRUENAS_API_KEY=<key>
```

### Certification Checklist

- [ ] Container images built with UBI base
- [ ] Required labels added to all images
- [ ] License file at `/licenses/LICENSE`
- [ ] Driver set as "Privileged" in Partner Connect
- [ ] Preflight passes for all containers
- [ ] Scorecard passes (5/5)
- [ ] Unit tests pass
- [ ] E2E tests pass
- [ ] Integration tests pass (11/11)
- [ ] CSI capabilities manifest created
- [ ] OpenShift E2E CSI tests pass
- [ ] Documentation complete
- [ ] Submitted to Partner Connect
