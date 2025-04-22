#!/bin/bash

set -e

CONFIG_FILE="/Users/aayush.senapati/development/cod/cluster-setup/ymls/k3.yaml"
CRD_FILE="/Users/aayush.senapati/development/cod/cluster-setup/ymls/crd.yaml"
ADMISSION_FILE="/Users/aayush.senapati/development/cod/cluster-setup/ymls/admission.yaml"
OPERATOR_FILE="/Users/aayush.senapati/development/cod/cluster-setup/ymls/operator.yaml"
CLUSTER_FILE="/Users/aayush.senapati/development/cod/cluster-setup/ymls/couchbase-cluster.yaml"

create_cluster() {
  local name=$1
  local num_servers=${2:-1}
  local num_agents=${3:-2}
  local k3d_context="k3d-$name"

  if ! k3d cluster list | grep -q "$name"; then
    cat <<EOF > $CONFIG_FILE
apiVersion: k3d.io/v1alpha5
kind: Simple
metadata:
  name: $name
servers: $num_servers
agents: $num_agents
volumes:
  - volume: /Users/aayush.senapati/development:/mnt/development
    nodeFilters:
      - agent:*
      - server:*
EOF

    k3d cluster create -i rancher/k3s:latest --config $CONFIG_FILE

    docker pull couchbase/server:7.6.0 || true
    k3d image import couchbase/server:7.6.0 -c $name

    docker pull couchbase/operator:2.8.0 || true
    k3d image import couchbase/operator:2.8.0 -c $name

    # Check if cod:latest image exists locally
    if ! docker image inspect cod:latest &> /dev/null; then
        echo "Error: Docker image 'cod:latest' not found locally."
        echo "Please build the image using the provided Dockerfile before running this script."
        exit 1
    fi
    k3d image import cod:latest -c $name

    echo "Setting kubectl context to $k3d_context..."
    kubectl config use-context "$k3d_context"

    echo "Applying Kubernetes manifests..."
    kubectl apply -f $CRD_FILE
    kubectl apply -f $ADMISSION_FILE
    kubectl apply -f $OPERATOR_FILE

    echo "Waiting for operator pods to be ready..."
    if kubectl wait --for=condition=ready pod -l app=couchbase-operator --timeout=300s; then
      echo "Operator pods are ready. Waiting for network to stabilize..."
      for i in {10..1}; do
        echo "Waiting for network: $i"
        sleep 1
      done
      kubectl apply -f $CLUSTER_FILE
    else
      echo "Operator pods did not become ready in time."
      exit 1
    fi
  else
    echo "Cluster $name already exists."
    echo "Ensuring context is set to $k3d_context..."
    kubectl config use-context "$k3d_context" || echo "Warning: Failed to set context for existing cluster $name."
  fi
}

delete_cluster() {
  local name=$1
  k3d cluster delete $name
}

if [ "$1" == "create" ]; then
  create_cluster "$2" "$3" "$4"
elif [ "$1" == "delete" ]; then
  delete_cluster "$2"
else
  echo "Usage: $0 {create|delete} <name> [num_servers] [num_agents]"
fi