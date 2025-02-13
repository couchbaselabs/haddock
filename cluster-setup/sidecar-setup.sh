#!/bin/bash

# Update package lists
apt-get update

# Install required packages
apt-get install -y curl iputils-ping xz-utils

# Run the Devbox installation script
curl -fsSL https://get.jetify.com/devbox | bash