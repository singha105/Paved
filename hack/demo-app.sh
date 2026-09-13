#!/usr/bin/env bash
#
# demo-app.sh: build the demo app image and load it into the paved k3d cluster, so the
# example claims run without a container registry. Safe to re-run.
set -euo pipefail

CLUSTER_NAME="${CLUSTER_NAME:-paved}"
IMAGE="${IMAGE:-paved-demo-app:0.1.0}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

echo "==> Building $IMAGE"
docker build --file "$ROOT/demo/app/Dockerfile" --tag "$IMAGE" "$ROOT"

echo "==> Importing $IMAGE into k3d cluster '$CLUSTER_NAME'"
k3d image import "$IMAGE" --cluster "$CLUSTER_NAME"
