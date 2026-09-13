#!/usr/bin/env bash
#
# testsvc-image.sh: build the test service image and push it to the paved cluster's registry,
# so example claims can pull it. Safe to re-run.
set -euo pipefail

REGISTRY_PORT="${REGISTRY_PORT:-5001}"
TAG="${TAG:-0.1.1}"
ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"

# One registry, two names: localhost from this machine, the container name from inside the cluster.
PUSH_REF="localhost:${REGISTRY_PORT}/testsvc:${TAG}"
PULL_REF="k3d-paved-registry:${REGISTRY_PORT}/testsvc:${TAG}"

echo "==> Building $PUSH_REF"
docker build --file "$ROOT/examples/testsvc/Dockerfile" --tag "$PUSH_REF" "$ROOT"

echo "==> Pushing $PUSH_REF"
docker push "$PUSH_REF"

echo "==> Claims reference this image as $PULL_REF"
