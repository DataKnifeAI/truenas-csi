# TrueNAS CSI Driver - Upgrade Guide

This guide covers upgrading the TrueNAS CSI Driver on OpenShift.

## Upgrade Methods

### Method 1: Automatic Updates via OLM (Recommended)

If you installed via OperatorHub with automatic updates enabled:

1. Navigate to **Operators** > **Installed Operators**
2. Find **TrueNAS CSI Driver**
3. Check the **Subscription** tab for pending updates
4. Updates are applied automatically based on your approval strategy

### Method 2: Manual Update via OLM

If using manual approval strategy:

1. Navigate to **Operators** > **Installed Operators**
2. Find **TrueNAS CSI Driver**
3. Go to the **Subscription** tab
4. Click **Upgrade available** if a new version exists
5. Review the changes and click **Approve**

### Method 3: CLI Update

```bash
# Check current version
oc get csv -n openshift-operators | grep truenas

# View available versions in the catalog
oc get packagemanifests truenas-csi-operator -o jsonpath='{.status.channels[*].currentCSV}'

# Update the subscription to a specific version
oc patch subscription truenas-csi-operator -n openshift-operators \
  --type merge \
  -p '{"spec":{"startingCSV":"truenas-csi-operator.v0.2.0"}}'
```

## Pre-Upgrade Checklist

Before upgrading:

1. **Backup TrueNASCSI CR configuration**
   ```bash
   oc get truenascsi truenas -o yaml > truenas-backup.yaml
   ```

2. **Check for pending volume operations**
   ```bash
   # Ensure no volumes are being provisioned/deleted
   oc get pvc --all-namespaces | grep -i pending
   ```

3. **Review release notes** for breaking changes

4. **Test in non-production** environment first

## Upgrade Process

### What Happens During Upgrade

1. OLM installs the new operator version
2. Old operator pod terminates
3. New operator pod starts
4. Operator reconciles TrueNASCSI resources
5. Controller and node pods are updated (rolling update)

### Expected Behavior

- **Controller pods**: Rolling update with zero downtime
- **Node pods**: Rolling update, one node at a time
- **Existing volumes**: Remain mounted and accessible
- **New provisioning**: Brief pause during controller restart

### Monitoring the Upgrade

```bash
# Watch operator pod
oc get pods -n openshift-operators -w | grep truenas

# Watch CSI pods
oc get pods -n truenas-csi -w

# Check operator logs
oc logs -n openshift-operators deployment/truenas-csi-operator-controller-manager -f

# Check TrueNASCSI status
oc get truenascsi truenas -o yaml
```

## Version Compatibility

### Operator and Driver Versions

| Operator Version | Driver Version | OpenShift | Notes |
|-----------------|----------------|-----------|-------|
| 0.1.0 | 0.1.0 | 4.20 | Initial release |

### Kubernetes API Compatibility

The CRD uses `v1alpha1` API version. Future versions may introduce `v1beta1` or `v1` with conversion webhooks for backward compatibility.

## Rollback Procedure

If issues occur after upgrade:

### Via OLM

1. Navigate to **Operators** > **Installed Operators**
2. Find **TrueNAS CSI Driver**
3. Go to **Subscription** tab
4. Click **Actions** > **Edit Subscription**
5. Set **Starting CSV** to previous version

### Via CLI

```bash
# List installed CSVs
oc get csv -n openshift-operators | grep truenas

# Delete current CSV
oc delete csv truenas-csi-operator.v0.2.0 -n openshift-operators

# OLM will reinstall from subscription
# Or manually specify previous version:
oc patch subscription truenas-csi-operator -n openshift-operators \
  --type merge \
  -p '{"spec":{"startingCSV":"truenas-csi-operator.v0.1.0"}}'
```

## Troubleshooting Upgrades

### Operator Pod Not Starting

```bash
# Check operator pod status
oc describe pod -n openshift-operators -l control-plane=controller-manager

# Check operator logs
oc logs -n openshift-operators deployment/truenas-csi-operator-controller-manager
```

### CSI Pods Not Updating

```bash
# Check TrueNASCSI status
oc get truenascsi truenas -o jsonpath='{.status}'

# Force reconciliation by updating annotation
oc annotate truenascsi truenas reconcile=$(date +%s) --overwrite
```

### Volumes Not Mounting After Upgrade

```bash
# Check node pod logs
oc logs -n truenas-csi daemonset/truenas-csi-node -c csi-node

# Verify CSIDriver registration
oc get csidriver csi.truenas.io

# Check node CSI socket
oc debug node/<node-name> -- ls -la /var/lib/kubelet/plugins/csi.truenas.io/
```

## OpenShift Version Upgrades

When upgrading OpenShift itself:

1. **Check CSI driver compatibility** with target OpenShift version
2. **Upgrade OpenShift** following Red Hat documentation
3. **Verify CSI driver** is functioning after OpenShift upgrade
4. **Update CSI driver** if a newer compatible version is available

### CSI Sidecar Updates

The operator uses OpenShift-provided CSI sidecars. When upgrading OpenShift:

- Sidecars are automatically updated by OpenShift
- The operator references version-specific sidecar images
- Operator updates may be required for new OpenShift versions

## Best Practices

1. **Schedule upgrades** during maintenance windows
2. **Monitor volume operations** before and after upgrade
3. **Keep backups** of TrueNASCSI configuration
4. **Test upgrades** in development/staging first
5. **Review release notes** for every version
6. **Maintain operator subscription** for security updates
