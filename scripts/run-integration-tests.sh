#!/bin/bash
set -e

echo "=== Building integration test container ==="
docker build -f Dockerfile.test -t ghissues-integration .

echo ""
echo "=== Running integration tests ==="
docker run --rm \
    --privileged \
    --device /dev/fuse \
    ghissues-integration

echo ""
echo "=== All integration tests passed ==="
