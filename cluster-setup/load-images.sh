#!/bin/bash

set -e

# List of images to import into the k3d cluster
IMAGES=(
  "couchbase/server:7.6.0"
  "aayushsenapati/couch:operator-arm"
  "aayushsenapati/couch:cod-arm"
)

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

echo "Importing images into cluster '$CLUSTER_NAME'..."

for IMAGE in "${IMAGES[@]}"; do
  echo "Processing image: $IMAGE"
  # Check if the image exists locally
  if ! docker image inspect "$IMAGE" &> /dev/null; then
    echo "Warning: Docker image '$IMAGE' not found locally. Pulling..."
    if ! docker pull "$IMAGE"; then
        echo "Error: Failed to pull image '$IMAGE'. Please ensure it exists or build it if necessary (e.g., cod:latest)."
        # Decide whether to exit or continue with other images
        # exit 1 
        continue # Continue to the next image
    fi
  fi
  
  # Import the image into the k3d cluster
  echo "Importing $IMAGE into $CLUSTER_NAME..."
  if ! k3d image import "$IMAGE" -c "$CLUSTER_NAME"; then
    echo "Error: Failed to import image '$IMAGE' into cluster '$CLUSTER_NAME'."
    # Decide whether to exit or continue
    # exit 1 
  else
    echo "Successfully imported $IMAGE."
  fi
done

echo "Image loading process completed for cluster '$CLUSTER_NAME'." 