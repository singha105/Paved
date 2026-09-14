#!/usr/bin/env bash
#
# shortlink-image.sh: build services/shortlink and push its image to the paved cluster's registry,
# where the shortlink claim pulls it from. Safe to re-run.
set -euo pipefail

REGISTRY_PORT="${REGISTRY_PORT:-5001}"
TAG="${TAG:-0.1.0}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# One registry, two names: localhost from this machine, the container name from inside the cluster.
PUSH_REF="localhost:${REGISTRY_PORT}/shortlink:${TAG}"
PULL_REF="k3d-paved-registry:${REGISTRY_PORT}/shortlink:${TAG}"

echo "==> Building $PUSH_REF"
docker build --tag "$PUSH_REF" "$ROOT/services/shortlink"

echo "==> Pushing $PUSH_REF"
docker push "$PUSH_REF"

echo "==> The shortlink claim references this image as $PULL_REF"
