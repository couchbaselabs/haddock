#!/bin/bash

set -e

CONFIG_FILE="/Users/aayush.senapati/development/ymls/k3.yaml"
CRD_FILE="/Users/aayush.senapati/development/ymls/crd.yaml"
ADMISSION_FILE="/Users/aayush.senapati/development/ymls/admission.yaml"
OPERATOR_FILE="/Users/aayush.senapati/development/ymls/operator.yaml"
CLUSTER_FILE="/Users/aayush.senapati/development/ymls/couchbase-cluster.yaml"

create_cluster() {
  local name=$1
  local num_servers=${2:-1}
  local num_agents=${3:-2}

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

    k3d cluster create --config $CONFIG_FILE

    docker pull couchbase/server:7.6.0 || true
    k3d image import couchbase/server:7.6.0 -c $name

    docker pull couchbase/operator:2.7.0 || true
    k3d image import couchbase/operator:2.7.0 -c $name

    docker pull ubuntu || true
    k3d image import ubuntu -c $name

    # docker pull prom/prometheus:latest || true
    # k3d image import prom/prometheus:latest -c $name

    # docker pull grafana/grafana:latest || true
    # k3d image import grafana/grafana:latest -c $name

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