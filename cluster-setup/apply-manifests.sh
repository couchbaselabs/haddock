#!/bin/bash

set -e

# Define paths to YAML files (adjust if necessary)
CRD_FILE="/Users/aayush.senapati/development/cod/cluster-setup/ymls/crd.yaml"
ADMISSION_FILE="/Users/aayush.senapati/development/cod/cluster-setup/ymls/admission.yaml"
OPERATOR_FILE="/Users/aayush.senapati/development/cod/cluster-setup/ymls/operator.yaml"
CLUSTER_FILE="/Users/aayush.senapati/development/cod/cluster-setup/ymls/couchbase-cluster.yaml"

# Check if cluster name is provided
if [ -z "$1" ]; then
  echo "Usage: $0 <cluster_name>"
  exit 1
fi

CLUSTER_NAME=$1
K3D_CONTEXT="k3d-$CLUSTER_NAME"

echo "Setting kubectl context to $K3D_CONTEXT..."
if ! kubectl config use-context "$K3D_CONTEXT"; then
  echo "Error: Failed to set kubectl context to $K3D_CONTEXT."
  echo "Make sure the cluster '$CLUSTER_NAME' exists and the context is configured correctly."
  exit 1
fi

echo "Applying Kubernetes manifests to cluster '$CLUSTER_NAME'..."

# Check if files exist before applying
if [ ! -f "$CRD_FILE" ] || [ ! -f "$ADMISSION_FILE" ] || [ ! -f "$OPERATOR_FILE" ] || [ ! -f "$CLUSTER_FILE" ]; then
    echo "Error: One or more YAML manifest files not found. Please check the paths:"
    echo "CRD: $CRD_FILE"
    echo "Admission: $ADMISSION_FILE"
    echo "Operator: $OPERATOR_FILE"
    echo "Cluster: $CLUSTER_FILE"
    exit 1
fi

kube_apply() {
    local file=$1
    echo "Applying $file..."
    if ! kubectl apply -f "$file"; then
        echo "Error applying $file."
        exit 1
    fi
}

kube_apply "$CRD_FILE"
kube_apply "$ADMISSION_FILE"
kube_apply "$OPERATOR_FILE"

echo "Waiting for operator pods to be ready..."
# Use the default namespace unless specified otherwise by the operator manifest
OPERATOR_NAMESPACE="default"
if kubectl wait --namespace "$OPERATOR_NAMESPACE" --for=condition=ready pod -l app=couchbase-operator --timeout=300s; then
  echo "Operator pods are ready. Waiting a few seconds for network/services to stabilize..."
  sleep 10 # Give some extra time
  kube_apply "$CLUSTER_FILE"
  echo "Successfully applied all manifests."
else
  echo "Error: Operator pods (in namespace '$OPERATOR_NAMESPACE' with label app=couchbase-operator) did not become ready within 5 minutes."
  echo "Please check the operator pod logs for errors:
kubectl logs -n $OPERATOR_NAMESPACE -l app=couchbase-operator"
  exit 1
fi

echo "Manifest application process completed for cluster '$CLUSTER_NAME'." 