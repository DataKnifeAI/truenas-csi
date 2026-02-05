#!/bin/bash
set -e

# TrueNAS CSI Driver - OpenShift Demo
# This demo tests the CSI driver on OpenShift (CRC/OpenShift Local)
# Deploys via the TrueNAS CSI Operator
#
# Environment Variables (optional - will prompt if not set):
#   TRUENAS_IP       - TrueNAS IP address (e.g., 192.168.1.100)
#   TRUENAS_API_KEY  - TrueNAS API key
#   TRUENAS_POOL     - Default ZFS pool (default: tank)
#   TRUENAS_INSECURE - Skip TLS verification (default: true)
#
# Example:
#   export TRUENAS_IP=192.168.1.100
#   export TRUENAS_API_KEY=1-abcdef1234567890
#   export TRUENAS_POOL=tank
#   ./demo-openshift.sh

NAMESPACE="truenas-csi"
OPERATOR_NAMESPACE="openshift-operators"
DEMO_NAMESPACE="demo"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
CYAN='\033[0;36m'
BOLD='\033[1m'
NC='\033[0m' # No Color

# Print colored output
print_info() {
    echo -e "${BLUE}[i]${NC} $1"
}

print_success() {
    echo -e "${GREEN}[+]${NC} $1"
}

print_warning() {
    echo -e "${YELLOW}[!]${NC} $1"
}

print_error() {
    echo -e "${RED}[-]${NC} $1"
}

print_header() {
    echo ""
    echo -e "${GREEN}${BOLD}======================================================================${NC}"
    echo -e "${GREEN}${BOLD}  $1${NC}"
    echo -e "${GREEN}${BOLD}======================================================================${NC}"
    echo ""
}

print_step() {
    echo ""
    echo -e "${CYAN}${BOLD}>> $1${NC}"
    echo ""
}

# Check prerequisites
check_prerequisites() {
    local missing=0

    print_step "Checking prerequisites"

    if ! command -v oc &> /dev/null; then
        print_error "oc (OpenShift CLI) not found"
        print_info "Install from: https://mirror.openshift.com/pub/openshift-v4/clients/ocp/latest/"
        missing=1
    else
        print_success "oc CLI found: $(oc version --client -o json 2>/dev/null | grep -o '"gitVersion":"[^"]*"' | cut -d'"' -f4 || echo "unknown version")"
    fi

    if ! command -v docker &> /dev/null && ! command -v podman &> /dev/null; then
        print_error "docker or podman not found"
        missing=1
    else
        if command -v podman &> /dev/null; then
            print_success "podman found"
        else
            print_success "docker found"
        fi
    fi

    if [ $missing -eq 1 ]; then
        print_error "Please install missing prerequisites and try again"
        exit 1
    fi

    # Check if logged into OpenShift
    if ! oc whoami &>/dev/null; then
        print_error "Not logged into OpenShift cluster"
        print_info "Please login first:"
        echo "  oc login -u kubeadmin https://api.crc.testing:6443"
        echo ""
        print_info "Or for CRC:"
        echo "  eval \$(crc oc-env)"
        echo "  oc login -u kubeadmin -p \$(crc console --credentials | grep kubeadmin | awk '{print \$NF}') https://api.crc.testing:6443"
        exit 1
    fi

    CLUSTER_USER=$(oc whoami)
    CLUSTER_SERVER=$(oc whoami --show-server)
    print_success "Logged into OpenShift as: ${CLUSTER_USER}"
    print_success "Cluster: ${CLUSTER_SERVER}"
    echo ""
}

# Check if operator and driver are deployed
check_deployment_status() {
    local operator_exists=false
    local driver_exists=false

    # Check if operator is deployed
    if oc get deployment -n ${OPERATOR_NAMESPACE} truenas-csi-operator-controller-manager &>/dev/null 2>&1; then
        operator_exists=true
    fi

    # Check if TrueNASCSI CR exists
    if oc get truenascsi truenas &>/dev/null 2>&1; then
        driver_exists=true
    fi

    if [ "$operator_exists" = true ] && [ "$driver_exists" = true ]; then
        return 0  # Both exist
    elif [ "$operator_exists" = false ]; then
        return 1  # Operator doesn't exist
    else
        return 2  # Operator exists but CR doesn't
    fi
}

# Collect TrueNAS configuration from env vars or prompt
collect_truenas_config() {
    print_header "TrueNAS Configuration"

    # Check for existing secret
    if oc get secret truenas-api-credentials -n ${NAMESPACE} &>/dev/null 2>&1; then
        print_info "Found existing credentials secret"
        # If we have env vars, ask if user wants to update
        if [ -n "$TRUENAS_IP" ] && [ -n "$TRUENAS_API_KEY" ]; then
            print_info "Environment variables detected"
            read -p "Update existing configuration with env vars? [y/N]: " UPDATE_CONFIG
            if [[ ! $UPDATE_CONFIG =~ ^[Yy]$ ]]; then
                # Use existing config
                if oc get truenascsi truenas &>/dev/null 2>&1; then
                    TRUENAS_URL=$(oc get truenascsi truenas -o jsonpath='{.spec.truenasURL}')
                    TRUENAS_POOL=$(oc get truenascsi truenas -o jsonpath='{.spec.defaultPool}')
                    TRUENAS_NFS_SERVER=$(oc get truenascsi truenas -o jsonpath='{.spec.nfsServer}')
                    TRUENAS_ISCSI_PORTAL=$(oc get truenascsi truenas -o jsonpath='{.spec.iscsiPortal}')
                    print_success "Using existing configuration"
                    return 0
                fi
            fi
        else
            read -p "Use existing configuration? [Y/n]: " USE_EXISTING
            if [[ ! $USE_EXISTING =~ ^[Nn]$ ]]; then
                if oc get truenascsi truenas &>/dev/null 2>&1; then
                    TRUENAS_URL=$(oc get truenascsi truenas -o jsonpath='{.spec.truenasURL}')
                    TRUENAS_POOL=$(oc get truenascsi truenas -o jsonpath='{.spec.defaultPool}')
                    TRUENAS_NFS_SERVER=$(oc get truenascsi truenas -o jsonpath='{.spec.nfsServer}')
                    TRUENAS_ISCSI_PORTAL=$(oc get truenascsi truenas -o jsonpath='{.spec.iscsiPortal}')
                    print_success "Using existing configuration"
                    return 0
                fi
            fi
        fi
    fi

    # Check environment variables first
    if [ -n "$TRUENAS_IP" ]; then
        print_success "Using TRUENAS_IP from environment: ${TRUENAS_IP}"
    else
        read -p "TrueNAS IP address (e.g., 192.168.1.100): " TRUENAS_IP
        if [ -z "$TRUENAS_IP" ]; then
            print_error "TrueNAS IP is required"
            print_info "You can also set TRUENAS_IP environment variable"
            exit 1
        fi
    fi

    TRUENAS_URL="wss://${TRUENAS_IP}/api/current"
    TRUENAS_NFS_SERVER="${TRUENAS_IP}"
    TRUENAS_ISCSI_PORTAL="${TRUENAS_IP}:3260"

    if [ -n "$TRUENAS_API_KEY" ]; then
        print_success "Using TRUENAS_API_KEY from environment"
    else
        read -p "TrueNAS API Key: " TRUENAS_API_KEY
        if [ -z "$TRUENAS_API_KEY" ]; then
            print_error "API key is required"
            print_info "You can also set TRUENAS_API_KEY environment variable"
            exit 1
        fi
    fi

    if [ -n "$TRUENAS_POOL" ]; then
        print_success "Using TRUENAS_POOL from environment: ${TRUENAS_POOL}"
    else
        read -p "Default ZFS pool name [tank]: " TRUENAS_POOL_INPUT
        TRUENAS_POOL="${TRUENAS_POOL_INPUT:-tank}"
    fi

    # Handle TRUENAS_INSECURE env var
    if [ -n "$TRUENAS_INSECURE" ]; then
        print_success "Using TRUENAS_INSECURE from environment: ${TRUENAS_INSECURE}"
    else
        read -p "Skip TLS verification? [Y/n]: " SKIP_TLS
        if [[ $SKIP_TLS =~ ^[Nn]$ ]]; then
            TRUENAS_INSECURE="false"
        else
            TRUENAS_INSECURE="true"
        fi
    fi

    echo ""
    print_success "Configuration:"
    echo ""
    echo "  URL: ${TRUENAS_URL}"
    echo "  Pool: ${TRUENAS_POOL}"
    echo "  NFS Server: ${TRUENAS_NFS_SERVER}"
    echo "  iSCSI Portal: ${TRUENAS_ISCSI_PORTAL}"
    echo "  Skip TLS: ${TRUENAS_INSECURE}"
    echo ""
}

# Deploy the operator
deploy_operator() {
    print_header "Deploying TrueNAS CSI Operator"

    # Apply SCC
    print_step "1. Applying SecurityContextConstraints"
    if [ -f "deploy/openshift/scc.yaml" ]; then
        oc apply -f deploy/openshift/scc.yaml
        print_success "SCC applied"
    else
        print_warning "SCC file not found, skipping"
    fi

    # Build and push operator image (or use existing)
    print_step "2. Deploying operator"

    # Check if we should build locally or use existing image
    read -p "Build operator locally or use existing image? [build/existing]: " BUILD_CHOICE

    if [[ $BUILD_CHOICE =~ ^[Bb]uild$ ]]; then
        print_info "Building operator image..."
        make operator-build IMG=quay.io/truenas_solutions/truenas-csi-operator:v0.1.0

        print_info "Pushing operator image..."
        make operator-push IMG=quay.io/truenas_solutions/truenas-csi-operator:v0.1.0
    fi

    # Deploy operator using kustomize
    print_info "Deploying operator to cluster..."
    cd operator && make deploy IMG=quay.io/truenas_solutions/truenas-csi-operator:v0.1.0
    cd ..

    # Wait for operator to be ready
    print_info "Waiting for operator to be ready..."
    oc wait --namespace=${OPERATOR_NAMESPACE} \
        --for=condition=Available \
        deployment/truenas-csi-operator-controller-manager \
        --timeout=120s

    print_success "Operator deployed successfully"
}

# Create the CSI driver via TrueNASCSI CR
deploy_csi_driver() {
    print_header "Deploying TrueNAS CSI Driver"

    # Create namespace if needed
    print_step "1. Creating namespace"
    oc create namespace ${NAMESPACE} --dry-run=client -o yaml | oc apply -f -
    print_success "Namespace ready: ${NAMESPACE}"

    # Create credentials secret
    print_step "2. Creating credentials secret"
    cat <<EOF | oc apply -f -
apiVersion: v1
kind: Secret
metadata:
  name: truenas-api-credentials
  namespace: ${NAMESPACE}
type: Opaque
stringData:
  api-key: "${TRUENAS_API_KEY}"
EOF
    print_success "Credentials secret created"

    # Create TrueNASCSI CR
    print_step "3. Creating TrueNASCSI custom resource"
    cat <<EOF | oc apply -f -
apiVersion: csi.truenas.io/v1alpha1
kind: TrueNASCSI
metadata:
  name: truenas
spec:
  truenasURL: "${TRUENAS_URL}"
  credentialsSecret: "truenas-api-credentials"
  defaultPool: "${TRUENAS_POOL}"
  nfsServer: "${TRUENAS_NFS_SERVER}"
  iscsiPortal: "${TRUENAS_ISCSI_PORTAL}"
  insecureSkipTLS: ${TRUENAS_INSECURE:-true}
  namespace: "${NAMESPACE}"
EOF
    print_success "TrueNASCSI CR created"

    # Wait for CSI driver to be ready
    print_step "4. Waiting for CSI driver to be ready"
    print_info "This may take up to 3 minutes..."

    # Wait for controller
    for i in {1..36}; do
        if oc wait --namespace=${NAMESPACE} \
            --for=condition=ready pod \
            --selector=app=truenas-csi-controller \
            --timeout=5s 2>/dev/null; then
            print_success "Controller pod is ready"
            break
        fi
        echo -ne "\r  Waiting for controller... (${i}0s)"
        sleep 5
    done
    echo ""

    # Wait for node pods
    for i in {1..36}; do
        if oc wait --namespace=${NAMESPACE} \
            --for=condition=ready pod \
            --selector=app=truenas-csi-node \
            --timeout=5s 2>/dev/null; then
            print_success "Node pods are ready"
            break
        fi
        echo -ne "\r  Waiting for node pods... (${i}0s)"
        sleep 5
    done
    echo ""

    # Show status
    print_step "5. CSI Driver Status"
    oc get truenascsi truenas
    echo ""
    oc get pods -n ${NAMESPACE}

    print_success "CSI driver deployed successfully"
}

# Setup storage classes
setup_storage_classes() {
    print_header "Setting Up Storage Classes"

    # Create demo namespace
    print_info "Creating demo namespace..."
    oc create namespace ${DEMO_NAMESPACE} --dry-run=client -o yaml | oc apply -f -

    # Create NFS StorageClass
    print_info "Creating NFS StorageClass..."
    cat <<EOF | oc apply -f -
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: truenas-nfs
provisioner: csi.truenas.io
parameters:
  protocol: "nfs"
  pool: "${TRUENAS_POOL}"
  compression: "lz4"
allowVolumeExpansion: true
reclaimPolicy: Delete
volumeBindingMode: Immediate
EOF

    # Create iSCSI StorageClass
    print_info "Creating iSCSI StorageClass..."
    cat <<EOF | oc apply -f -
apiVersion: storage.k8s.io/v1
kind: StorageClass
metadata:
  name: truenas-iscsi
provisioner: csi.truenas.io
parameters:
  protocol: "iscsi"
  pool: "${TRUENAS_POOL}"
  compression: "lz4"
  volblocksize: "16K"
allowVolumeExpansion: true
reclaimPolicy: Delete
volumeBindingMode: Immediate
EOF

    print_success "Storage classes created"
    echo ""
    oc get storageclass | grep truenas
}

# Setup snapshot support
setup_snapshot_support() {
    print_header "Setting Up Snapshot Support"

    # OpenShift 4.x includes snapshot CRDs, just need VolumeSnapshotClass
    print_info "Creating VolumeSnapshotClass..."
    cat <<EOF | oc apply -f -
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshotClass
metadata:
  name: truenas-snapshot-class
driver: csi.truenas.io
deletionPolicy: Delete
EOF

    print_success "Snapshot support configured"
}

# Run setup if needed
run_setup_if_needed() {
    check_prerequisites

    # Check deployment status
    set +e
    check_deployment_status
    local status=$?
    set -e

    if [ $status -eq 0 ]; then
        print_success "Operator and CSI driver already deployed!"

        # Load existing config
        TRUENAS_URL=$(oc get truenascsi truenas -o jsonpath='{.spec.truenasURL}' 2>/dev/null)
        TRUENAS_POOL=$(oc get truenascsi truenas -o jsonpath='{.spec.defaultPool}' 2>/dev/null)
        TRUENAS_NFS_SERVER=$(oc get truenascsi truenas -o jsonpath='{.spec.nfsServer}' 2>/dev/null)
        TRUENAS_ISCSI_PORTAL=$(oc get truenascsi truenas -o jsonpath='{.spec.iscsiPortal}' 2>/dev/null)

        # Ensure storage classes exist
        if ! oc get storageclass truenas-nfs &>/dev/null; then
            setup_storage_classes
        fi
        if ! oc get volumesnapshotclass truenas-snapshot-class &>/dev/null; then
            setup_snapshot_support
        fi
        return 0
    fi

    print_header "Initial Setup Required"

    # Collect TrueNAS configuration
    collect_truenas_config

    if [ $status -eq 1 ]; then
        print_info "Deploying operator and CSI driver..."
        deploy_operator
        deploy_csi_driver
        setup_storage_classes
        setup_snapshot_support
    elif [ $status -eq 2 ]; then
        print_info "Operator exists, deploying CSI driver..."
        deploy_csi_driver
        setup_storage_classes
        setup_snapshot_support
    fi

    print_header "Setup Complete!"
    print_success "OpenShift environment is ready for demos"
}

# Demo: NFS Volume Provisioning
demo_nfs() {
    print_header "Demo: NFS Volume Provisioning"

    print_step "1. Creating NFS PersistentVolumeClaim (1Gi)"

    cat <<EOF | oc apply -f -
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: demo-nfs-pvc
  namespace: ${DEMO_NAMESPACE}
spec:
  accessModes:
    - ReadWriteMany
  storageClassName: truenas-nfs
  resources:
    requests:
      storage: 1Gi
EOF

    print_info "Waiting for PVC to be bound..."
    if oc wait --namespace=${DEMO_NAMESPACE} \
        --for=jsonpath='{.status.phase}'=Bound \
        pvc/demo-nfs-pvc \
        --timeout=60s; then

        print_success "PVC successfully bound!"
        echo ""

        print_step "2. PVC Details"
        oc get pvc -n ${DEMO_NAMESPACE} demo-nfs-pvc -o wide
        echo ""

        PV_NAME=$(oc get pvc -n ${DEMO_NAMESPACE} demo-nfs-pvc -o jsonpath='{.spec.volumeName}')
        print_step "3. Bound PersistentVolume"
        oc get pv ${PV_NAME}
        echo ""

        print_step "4. CSI Controller Logs"
        oc logs -n ${NAMESPACE} -l app=truenas-csi-controller -c csi-controller --tail=20 | grep -E "CreateVolume|NFS|dataset" || echo "(No matching log entries)"
        echo ""

        print_success "NFS volume created on TrueNAS!"
        echo ""
        echo -e "${BOLD}What happened:${NC}"
        echo "  - Kubernetes created a PVC requesting 1Gi of NFS storage"
        echo "  - CSI driver created a ZFS dataset on TrueNAS"
        echo "  - CSI driver created an NFS share for the dataset"
        echo "  - PVC is now bound and ready to use"
        echo ""
        echo -e "${CYAN}Check your TrueNAS UI:${NC}"
        echo "  Storage -> Datasets -> Look for the new dataset"
        echo "  Shares -> NFS -> Look for the new share"
    else
        print_error "PVC failed to bind"
        oc logs -n ${NAMESPACE} -l app=truenas-csi-controller -c csi-controller --tail=30
    fi

    echo ""
    read -p "Press Enter to continue..."
}

# Demo: iSCSI Volume Provisioning
demo_iscsi() {
    print_header "Demo: iSCSI Volume Provisioning"

    print_step "1. Creating iSCSI PersistentVolumeClaim (2Gi)"

    cat <<EOF | oc apply -f -
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: demo-iscsi-pvc
  namespace: ${DEMO_NAMESPACE}
spec:
  accessModes:
    - ReadWriteOnce
  storageClassName: truenas-iscsi
  resources:
    requests:
      storage: 2Gi
EOF

    print_info "Waiting for PVC to be bound..."
    if oc wait --namespace=${DEMO_NAMESPACE} \
        --for=jsonpath='{.status.phase}'=Bound \
        pvc/demo-iscsi-pvc \
        --timeout=60s; then

        print_success "PVC successfully bound!"
        echo ""

        print_step "2. PVC Details"
        oc get pvc -n ${DEMO_NAMESPACE} demo-iscsi-pvc -o wide
        echo ""

        PV_NAME=$(oc get pvc -n ${DEMO_NAMESPACE} demo-iscsi-pvc -o jsonpath='{.spec.volumeName}')
        print_step "3. Bound PersistentVolume"
        oc get pv ${PV_NAME}
        echo ""

        print_step "4. CSI Controller Logs"
        oc logs -n ${NAMESPACE} -l app=truenas-csi-controller -c csi-controller --tail=20 | grep -E "CreateVolume|iSCSI|ZVOL|target|extent" || echo "(No matching log entries)"
        echo ""

        print_success "iSCSI volume created on TrueNAS!"
        echo ""
        echo -e "${BOLD}What happened:${NC}"
        echo "  - Kubernetes created a PVC requesting 2Gi of iSCSI block storage"
        echo "  - CSI driver created a ZVOL on TrueNAS"
        echo "  - CSI driver created iSCSI target, extent, and association"
        echo "  - PVC is now bound and ready to use"
        echo ""
        echo -e "${CYAN}Check your TrueNAS UI:${NC}"
        echo "  Storage -> Datasets -> Look for the new ZVOL"
        echo "  Shares -> iSCSI -> Targets, Extents, Associated Targets"
    else
        print_error "PVC failed to bind"
        oc logs -n ${NAMESPACE} -l app=truenas-csi-controller -c csi-controller --tail=30
    fi

    echo ""
    read -p "Press Enter to continue..."
}

# Demo: Volume Expansion
demo_expand() {
    print_header "Demo: Volume Expansion"

    # Check for existing volumes
    PVCS=$(oc get pvc -n ${DEMO_NAMESPACE} -o jsonpath='{.items[?(@.status.phase=="Bound")].metadata.name}' 2>/dev/null)

    if [ -z "$PVCS" ]; then
        print_warning "No volumes found. Creating a test volume..."

        cat <<EOF | oc apply -f -
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: demo-expand-test
  namespace: ${DEMO_NAMESPACE}
spec:
  accessModes:
    - ReadWriteMany
  storageClassName: truenas-nfs
  resources:
    requests:
      storage: 1Gi
EOF

        oc wait --namespace=${DEMO_NAMESPACE} \
            --for=jsonpath='{.status.phase}'=Bound \
            pvc/demo-expand-test \
            --timeout=60s

        SELECTED_PVC="demo-expand-test"
        ORIGINAL_SIZE="1Gi"
    else
        echo "Available volumes:"
        PVC_ARRAY=($PVCS)
        for i in "${!PVC_ARRAY[@]}"; do
            PVC_NAME="${PVC_ARRAY[$i]}"
            PVC_SIZE=$(oc get pvc -n ${DEMO_NAMESPACE} $PVC_NAME -o jsonpath='{.status.capacity.storage}')
            echo "  $((i+1))) $PVC_NAME - ${PVC_SIZE}"
        done
        echo ""
        read -p "Select volume to expand [1-${#PVC_ARRAY[@]}]: " CHOICE

        if [[ "$CHOICE" =~ ^[1-9][0-9]*$ ]] && [ "$CHOICE" -le "${#PVC_ARRAY[@]}" ]; then
            SELECTED_PVC="${PVC_ARRAY[$((CHOICE-1))]}"
            ORIGINAL_SIZE=$(oc get pvc -n ${DEMO_NAMESPACE} $SELECTED_PVC -o jsonpath='{.status.capacity.storage}')
        else
            print_error "Invalid selection"
            return
        fi
    fi

    # Calculate new size
    ORIGINAL_SIZE_NUM=$(echo $ORIGINAL_SIZE | sed 's/Gi//')
    NEW_SIZE=$((ORIGINAL_SIZE_NUM + 1))
    NEW_SIZE="${NEW_SIZE}Gi"

    print_step "Expanding ${SELECTED_PVC} from ${ORIGINAL_SIZE} to ${NEW_SIZE}"

    oc patch pvc $SELECTED_PVC -n ${DEMO_NAMESPACE} \
        -p "{\"spec\":{\"resources\":{\"requests\":{\"storage\":\"${NEW_SIZE}\"}}}}"

    print_info "Waiting for expansion..."
    sleep 10

    ACTUAL_SIZE=$(oc get pvc -n ${DEMO_NAMESPACE} $SELECTED_PVC -o jsonpath='{.status.capacity.storage}')
    print_success "Volume expanded to ${ACTUAL_SIZE}!"
    echo ""

    oc get pvc -n ${DEMO_NAMESPACE} $SELECTED_PVC
    echo ""

    print_step "CSI Controller Logs"
    oc logs -n ${NAMESPACE} -l app=truenas-csi-controller -c csi-controller --tail=15 | grep -E "Expand|resize|quota" || echo "(No matching entries)"

    echo ""
    read -p "Press Enter to continue..."
}

# Demo: Volume Snapshots
demo_snapshot() {
    print_header "Demo: Volume Snapshots"

    # Get list of bound PVCs
    PVCS=$(oc get pvc -n ${DEMO_NAMESPACE} -o jsonpath='{.items[?(@.status.phase=="Bound")].metadata.name}' 2>/dev/null)

    if [ -z "$PVCS" ]; then
        print_warning "No volumes found. Please create a volume first."
        read -p "Press Enter to continue..."
        return
    fi

    echo "Available volumes:"
    PVC_ARRAY=($PVCS)
    for i in "${!PVC_ARRAY[@]}"; do
        PVC_NAME="${PVC_ARRAY[$i]}"
        PVC_SIZE=$(oc get pvc -n ${DEMO_NAMESPACE} $PVC_NAME -o jsonpath='{.status.capacity.storage}')
        echo "  $((i+1))) $PVC_NAME - ${PVC_SIZE}"
    done
    echo ""
    read -p "Select volume to snapshot [1-${#PVC_ARRAY[@]}]: " CHOICE

    if ! [[ "$CHOICE" =~ ^[0-9]+$ ]] || [ "$CHOICE" -lt 1 ] || [ "$CHOICE" -gt "${#PVC_ARRAY[@]}" ]; then
        print_error "Invalid selection"
        return
    fi

    SELECTED_PVC="${PVC_ARRAY[$((CHOICE-1))]}"
    print_success "Selected: $SELECTED_PVC"

    SNAPSHOT_NAME="snapshot-${SELECTED_PVC}-$(date +%s)"

    print_step "Creating VolumeSnapshot: ${SNAPSHOT_NAME}"

    cat <<EOF | oc apply -f -
apiVersion: snapshot.storage.k8s.io/v1
kind: VolumeSnapshot
metadata:
  name: ${SNAPSHOT_NAME}
  namespace: ${DEMO_NAMESPACE}
spec:
  volumeSnapshotClassName: truenas-snapshot-class
  source:
    persistentVolumeClaimName: ${SELECTED_PVC}
EOF

    print_info "Waiting for snapshot..."
    sleep 5

    oc get volumesnapshot -n ${DEMO_NAMESPACE} ${SNAPSHOT_NAME}
    echo ""

    SNAPSHOT_HANDLE=$(oc get volumesnapshot -n ${DEMO_NAMESPACE} ${SNAPSHOT_NAME} -o jsonpath='{.status.snapshotHandle}' 2>/dev/null)
    if [ -n "$SNAPSHOT_HANDLE" ]; then
        print_success "Snapshot created: ${SNAPSHOT_HANDLE}"
    fi

    echo ""
    echo -e "${CYAN}Check your TrueNAS UI:${NC}"
    echo "  Storage -> Snapshots"
    echo ""

    read -p "Restore a volume from this snapshot? [y/N]: " RESTORE
    if [[ $RESTORE =~ ^[Yy]$ ]]; then
        RESTORE_PVC="restored-${SELECTED_PVC}-$(date +%s)"
        RESTORE_SIZE=$(oc get pvc -n ${DEMO_NAMESPACE} ${SELECTED_PVC} -o jsonpath='{.status.capacity.storage}')
        RESTORE_CLASS=$(oc get pvc -n ${DEMO_NAMESPACE} ${SELECTED_PVC} -o jsonpath='{.spec.storageClassName}')
        RESTORE_ACCESS=$(oc get pvc -n ${DEMO_NAMESPACE} ${SELECTED_PVC} -o jsonpath='{.spec.accessModes[0]}')

        print_step "Restoring volume from snapshot"

        cat <<EOF | oc apply -f -
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: ${RESTORE_PVC}
  namespace: ${DEMO_NAMESPACE}
spec:
  accessModes:
    - ${RESTORE_ACCESS}
  storageClassName: ${RESTORE_CLASS}
  resources:
    requests:
      storage: ${RESTORE_SIZE}
  dataSource:
    name: ${SNAPSHOT_NAME}
    kind: VolumeSnapshot
    apiGroup: snapshot.storage.k8s.io
EOF

        print_info "Waiting for restore..."
        if oc wait --namespace=${DEMO_NAMESPACE} \
            --for=jsonpath='{.status.phase}'=Bound \
            pvc/${RESTORE_PVC} \
            --timeout=120s; then
            print_success "Volume restored: ${RESTORE_PVC}"
        else
            print_error "Restore failed"
        fi
    fi

    echo ""
    read -p "Press Enter to continue..."
}

# Demo: Volume Cloning
demo_clone() {
    print_header "Demo: Volume Cloning"

    PVCS=$(oc get pvc -n ${DEMO_NAMESPACE} -o jsonpath='{.items[?(@.status.phase=="Bound")].metadata.name}' 2>/dev/null)

    if [ -z "$PVCS" ]; then
        print_warning "No volumes found. Creating a source volume..."

        cat <<EOF | oc apply -f -
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: demo-clone-source
  namespace: ${DEMO_NAMESPACE}
spec:
  accessModes:
    - ReadWriteMany
  storageClassName: truenas-nfs
  resources:
    requests:
      storage: 1Gi
EOF

        oc wait --namespace=${DEMO_NAMESPACE} \
            --for=jsonpath='{.status.phase}'=Bound \
            pvc/demo-clone-source \
            --timeout=60s

        SELECTED_PVC="demo-clone-source"
    else
        echo "Available volumes:"
        PVC_ARRAY=($PVCS)
        for i in "${!PVC_ARRAY[@]}"; do
            PVC_NAME="${PVC_ARRAY[$i]}"
            PVC_SIZE=$(oc get pvc -n ${DEMO_NAMESPACE} $PVC_NAME -o jsonpath='{.status.capacity.storage}')
            echo "  $((i+1))) $PVC_NAME - ${PVC_SIZE}"
        done
        echo ""
        read -p "Select volume to clone [1-${#PVC_ARRAY[@]}]: " CHOICE

        if [[ "$CHOICE" =~ ^[1-9][0-9]*$ ]] && [ "$CHOICE" -le "${#PVC_ARRAY[@]}" ]; then
            SELECTED_PVC="${PVC_ARRAY[$((CHOICE-1))]}"
        else
            print_error "Invalid selection"
            return
        fi
    fi

    SELECTED_SIZE=$(oc get pvc -n ${DEMO_NAMESPACE} $SELECTED_PVC -o jsonpath='{.status.capacity.storage}')
    SELECTED_CLASS=$(oc get pvc -n ${DEMO_NAMESPACE} $SELECTED_PVC -o jsonpath='{.spec.storageClassName}')
    SELECTED_ACCESS=$(oc get pvc -n ${DEMO_NAMESPACE} $SELECTED_PVC -o jsonpath='{.spec.accessModes[0]}')

    CLONE_NAME="clone-${SELECTED_PVC}-$(date +%s)"

    print_step "Cloning ${SELECTED_PVC} to ${CLONE_NAME}"

    cat <<EOF | oc apply -f -
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: ${CLONE_NAME}
  namespace: ${DEMO_NAMESPACE}
spec:
  accessModes:
    - ${SELECTED_ACCESS}
  storageClassName: ${SELECTED_CLASS}
  resources:
    requests:
      storage: ${SELECTED_SIZE}
  dataSource:
    name: ${SELECTED_PVC}
    kind: PersistentVolumeClaim
EOF

    print_info "Waiting for clone to be ready..."
    if oc wait --namespace=${DEMO_NAMESPACE} \
        --for=jsonpath='{.status.phase}'=Bound \
        pvc/${CLONE_NAME} \
        --timeout=120s; then

        print_success "Clone created successfully!"
        echo ""
        oc get pvc -n ${DEMO_NAMESPACE} ${SELECTED_PVC} ${CLONE_NAME}
    else
        print_warning "Clone still in progress..."
        oc get pvc -n ${DEMO_NAMESPACE} ${CLONE_NAME}
    fi

    echo ""
    echo -e "${BOLD}How cloning works:${NC}"
    echo "  - CSI driver creates a ZFS snapshot of the source"
    echo "  - Clones a new dataset from the snapshot"
    echo "  - Clone is independent and can be modified separately"

    echo ""
    read -p "Press Enter to continue..."
}

# Show current status
show_status() {
    print_header "Current Status"

    print_step "TrueNASCSI Custom Resource"
    oc get truenascsi truenas -o wide 2>/dev/null || echo "Not found"
    echo ""

    print_step "Operator Pod"
    oc get pods -n ${OPERATOR_NAMESPACE} -l control-plane=controller-manager 2>/dev/null || echo "Not found"
    echo ""

    print_step "CSI Driver Pods"
    oc get pods -n ${NAMESPACE} 2>/dev/null || echo "Not found"
    echo ""

    print_step "Storage Classes"
    oc get storageclass | grep truenas 2>/dev/null || echo "None found"
    echo ""

    print_step "Demo PVCs"
    oc get pvc -n ${DEMO_NAMESPACE} 2>/dev/null || echo "None found"
    echo ""

    print_step "Volume Snapshots"
    oc get volumesnapshot -n ${DEMO_NAMESPACE} 2>/dev/null || echo "None found"
    echo ""
}

# View logs
view_logs() {
    print_header "CSI Driver Logs"

    echo -e "${CYAN}Controller logs (last 50 lines):${NC}"
    oc logs -n ${NAMESPACE} -l app=truenas-csi-controller -c csi-controller --tail=50 2>/dev/null || echo "Not available"
    echo ""

    read -p "View node logs? [y/N]: " VIEW_NODE
    if [[ $VIEW_NODE =~ ^[Yy]$ ]]; then
        echo ""
        echo -e "${CYAN}Node logs (last 30 lines):${NC}"
        oc logs -n ${NAMESPACE} -l app=truenas-csi-node -c csi-node --tail=30 2>/dev/null || echo "Not available"
    fi

    echo ""
    read -p "Press Enter to continue..."
}

# Cleanup
cleanup() {
    print_header "Cleanup Demo Resources"

    echo "This will delete all demo PVCs and volumes."
    read -p "Are you sure? [y/N]: " CONFIRM

    if [[ $CONFIRM =~ ^[Yy]$ ]]; then
        print_info "Deleting demo PVCs..."
        oc delete pvc -n ${DEMO_NAMESPACE} --all

        print_info "Deleting volume snapshots..."
        oc delete volumesnapshot -n ${DEMO_NAMESPACE} --all 2>/dev/null || true

        sleep 5
        print_success "Demo resources cleaned up"
        echo ""
        print_info "Check TrueNAS UI - datasets and shares should be removed"
    else
        print_info "Cleanup cancelled"
    fi

    echo ""
    read -p "Press Enter to continue..."
}

# Uninstall everything
uninstall() {
    print_header "Uninstall TrueNAS CSI Driver"

    echo "This will remove:"
    echo "  - All demo PVCs and volumes"
    echo "  - TrueNASCSI custom resource"
    echo "  - CSI driver pods"
    echo "  - Operator deployment"
    echo ""
    read -p "Are you sure? [y/N]: " CONFIRM

    if [[ ! $CONFIRM =~ ^[Yy]$ ]]; then
        print_info "Uninstall cancelled"
        return
    fi

    print_info "Cleaning up demo resources..."
    oc delete pvc -n ${DEMO_NAMESPACE} --all 2>/dev/null || true
    oc delete volumesnapshot -n ${DEMO_NAMESPACE} --all 2>/dev/null || true

    print_info "Deleting TrueNASCSI CR..."
    oc delete truenascsi truenas 2>/dev/null || true

    print_info "Waiting for CSI driver cleanup..."
    sleep 10

    print_info "Undeploying operator..."
    cd operator && make undeploy 2>/dev/null || true
    cd ..

    print_info "Deleting namespaces..."
    oc delete namespace ${NAMESPACE} --ignore-not-found=true
    oc delete namespace ${DEMO_NAMESPACE} --ignore-not-found=true

    print_success "Uninstall complete"
    echo ""
    read -p "Press Enter to continue..."
}

# Main menu
main_menu() {
    while true; do
        clear
        print_header "TrueNAS CSI Driver - OpenShift Demo"

        echo -e "${CYAN}Volume Provisioning:${NC}"
        echo "  1) Demo NFS volume creation"
        echo "  2) Demo iSCSI volume creation"
        echo ""
        echo -e "${CYAN}Advanced Operations:${NC}"
        echo "  3) Demo volume expansion"
        echo "  4) Demo volume snapshots"
        echo "  5) Demo volume cloning"
        echo ""
        echo -e "${CYAN}Utilities:${NC}"
        echo "  6) Show current status"
        echo "  7) View driver logs"
        echo "  8) Cleanup demo resources"
        echo "  9) Uninstall (remove everything)"
        echo "  0) Exit"
        echo ""
        read -p "Choose option: " OPTION

        case $OPTION in
            1) demo_nfs ;;
            2) demo_iscsi ;;
            3) demo_expand ;;
            4) demo_snapshot ;;
            5) demo_clone ;;
            6) show_status; read -p "Press Enter to continue..." ;;
            7) view_logs ;;
            8) cleanup ;;
            9) uninstall ;;
            0)
                print_info "Exiting..."
                exit 0
                ;;
            *)
                print_error "Invalid option"
                sleep 2
                ;;
        esac
    done
}

# Main
main() {
    print_header "TrueNAS CSI Driver - OpenShift Demo"

    # Show env var help if no args and no env vars
    if [ -z "$TRUENAS_IP" ] && [ -z "$TRUENAS_API_KEY" ]; then
        print_info "Tip: You can set environment variables to skip prompts:"
        echo ""
        echo "  export TRUENAS_IP=192.168.1.100"
        echo "  export TRUENAS_API_KEY=1-abcdef1234567890"
        echo "  export TRUENAS_POOL=tank"
        echo "  export TRUENAS_INSECURE=true"
        echo ""
    fi

    run_setup_if_needed

    echo ""
    print_info "This demo tests CSI driver features on OpenShift:"
    echo "  - NFS and iSCSI volume provisioning"
    echo "  - Volume expansion"
    echo "  - Volume snapshots and restore"
    echo "  - Volume cloning"
    echo ""
    print_info "Volumes are created on TrueNAS - check your TrueNAS UI!"
    echo ""

    read -p "Press Enter to start the demo menu..."

    main_menu
}

# Run
main
