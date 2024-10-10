#!/usr/bin/env bash

set -euo pipefail

printenv BUILDKITE_K8S_GCR_JSON_KEY > /tmp/gcr-k8s-key.json
gcloud auth activate-service-account --key-file=/tmp/gcr-k8s-key.json
gcloud auth configure-docker us-central1-docker.pkg.dev --quiet
# Start dockerd in the background
/usr/bin/dockerd --host=unix:///var/run/docker.sock --host=tcp://0.0.0.0:2375 --storage-driver=vfs > /var/log/dockerd.log 2>&1 &
# Wait for dockerd to initialize
echo "Waiting for Docker daemon to start..."
sleep 30
# Check if dockerd is running
if ! docker info > /dev/null 2>&1; then
    echo "Docker daemon failed to start. Check /var/log/dockerd.log for details."
    exit 1
fi