#!/bin/bash
set -e

echo "=== Building container ==="
docker build -t ghissues-test .

echo ""
echo "=== Running interactive container with FUSE support ==="
echo "Inside the container, run:"
echo "  export GITHUB_TOKEN=your_token"
echo "  ./ghissues mount JohanCodinha/ghissues /mnt/issues &"
echo "  sleep 2"
echo "  ls /mnt/issues/"
echo "  cat /mnt/issues/*.md"
echo ""

docker run -it --rm \
    --device /dev/fuse \
    --cap-add SYS_ADMIN \
    --security-opt apparmor:unconfined \
    -e GITHUB_TOKEN="${GITHUB_TOKEN}" \
    ghissues-test /bin/sh
