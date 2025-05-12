#!/bin/bash

set -e

CONFIG_FILE="/Users/aayush.senapati/development/cod/cluster-setup/ymls/k3.yaml"

create_cluster() {
  local name=$1
  local num_servers=${2:-1}
  local num_agents=${3:-2}
  local k3d_context="k3d-$name"

  if ! k3d cluster list | grep -q "$name"; then
    echo "Creating cluster $name..."
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

    echo "Setting kubectl context to $k3d_context..."
    kubectl config use-context "$k3d_context"
    echo "Cluster $name created and context set."
    echo "NOTE: Docker images were NOT imported."
    echo "NOTE: No manifests were applied. Please apply them manually."
  else
    echo "Cluster $name already exists."
    echo "Ensuring context is set to $k3d_context..."
    kubectl config use-context "$k3d_context" || echo "Warning: Failed to set context for existing cluster $name."
    echo "NOTE: No manifests were applied as cluster already exists. Please apply them manually if needed."
  fi
}

delete_cluster() {
  local name=$1
  echo "Deleting cluster $name..."
  k3d cluster delete $name
  echo "Cluster $name deleted."
}

if [ "$1" == "create" ]; then
  create_cluster "$2" "$3" "$4"
elif [ "$1" == "delete" ]; then
  delete_cluster "$2"
else
  echo "Usage: $0 {create|delete} <name> [num_servers] [num_agents]"
  echo "This script creates/deletes a k3d cluster and sets the kubectl context."
  echo "It does NOT import images or apply any Kubernetes manifests."
fi 