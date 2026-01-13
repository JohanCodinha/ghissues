#!/bin/bash
# Run E2E tests in a Docker container with FUSE support
set -e

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

# Get token from environment or gh CLI
if [ -z "$GITHUB_TOKEN" ]; then
    GITHUB_TOKEN=$(gh auth token 2>/dev/null || true)
fi

if [ -z "$GITHUB_TOKEN" ]; then
    echo "ERROR: No GitHub token found"
    echo "Set GITHUB_TOKEN or run 'gh auth login'"
    exit 1
fi

echo "=== Building E2E test container ==="
docker build -t ghissues-e2e -f - "$PROJECT_DIR" <<'DOCKERFILE'
FROM golang:alpine

RUN apk add --no-cache fuse3 fuse3-dev git bash curl

ENV GOTOOLCHAIN=auto
RUN echo "user_allow_other" >> /etc/fuse.conf

WORKDIR /app
COPY go.mod go.sum ./
RUN go mod download
COPY . .
RUN go build -o ghissues ./cmd/ghissues

# Install gh CLI
RUN curl -fsSL https://cli.github.com/packages/githubcli-archive-keyring.gpg -o /usr/share/keyrings/githubcli-archive-keyring.gpg 2>/dev/null || \
    (apk add --no-cache github-cli || true)

# Copy the e2e script
COPY scripts/e2e-real-github.sh /app/scripts/

ENTRYPOINT ["/bin/bash"]
DOCKERFILE

echo ""
echo "=== Running E2E tests in container ==="
docker run --rm \
    --privileged \
    --cap-add SYS_ADMIN \
    --device /dev/fuse \
    --security-opt apparmor:unconfined \
    -e GITHUB_TOKEN="$GITHUB_TOKEN" \
    -e GITHUB_REPOSITORY_OWNER="${GITHUB_REPOSITORY_OWNER:-JohanCodinha}" \
    -e GITHUB_RUN_ID="${GITHUB_RUN_ID:-local-$(date +%s)}" \
    ghissues-e2e \
    /app/scripts/e2e-real-github.sh
