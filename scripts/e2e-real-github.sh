#!/bin/bash
# E2E test against real GitHub API
# Creates a fresh test repo, runs tests, keeps repo for debugging
set -e

TEST_REPO_NAME="ghissues-e2e-test"
TEST_REPO_OWNER="${GITHUB_REPOSITORY_OWNER:-JohanCodinha}"
TEST_REPO="${TEST_REPO_OWNER}/${TEST_REPO_NAME}"

echo "=== E2E Test: Real GitHub API ==="
echo "Test repo: ${TEST_REPO}"
echo ""

# Check for required token
if [ -z "$GITHUB_TOKEN" ]; then
    echo "ERROR: GITHUB_TOKEN not set"
    echo "Set it with: export GITHUB_TOKEN=\$(gh auth token)"
    exit 1
fi

# Helper function to call GitHub API directly (for containers without gh CLI)
github_api() {
    local method="$1"
    local endpoint="$2"
    local data="$3"

    if [ -n "$data" ]; then
        curl -s -X "$method" \
            -H "Authorization: Bearer $GITHUB_TOKEN" \
            -H "Accept: application/vnd.github+json" \
            -H "Content-Type: application/json" \
            -d "$data" \
            "https://api.github.com$endpoint"
    else
        curl -s -X "$method" \
            -H "Authorization: Bearer $GITHUB_TOKEN" \
            -H "Accept: application/vnd.github+json" \
            "https://api.github.com$endpoint"
    fi
}

# Check if gh CLI is available
if command -v gh &>/dev/null; then
    echo "$GITHUB_TOKEN" | gh auth login --with-token 2>/dev/null || true
    USE_GH_CLI=true
else
    echo "Note: gh CLI not found, using direct API calls"
    USE_GH_CLI=false
fi

echo "=== Step 1: Cleanup old test repo (if exists) ==="
REPO_EXISTS=$(github_api GET "/repos/$TEST_REPO" 2>/dev/null | grep -c '"id"' || echo "0")
if [ "$REPO_EXISTS" -gt 0 ]; then
    echo "Deleting existing repo: $TEST_REPO"
    github_api DELETE "/repos/$TEST_REPO"
    echo "Deleted. Waiting for GitHub to process..."
    sleep 5
else
    echo "No existing repo found, skipping cleanup"
fi

echo ""
echo "=== Step 2: Create fresh test repo ==="
CREATE_RESULT=$(github_api POST "/user/repos" "{\"name\":\"$TEST_REPO_NAME\",\"description\":\"Temporary repo for ghissues E2E testing. Auto-deleted on next test run.\",\"auto_init\":false,\"private\":false}")

if echo "$CREATE_RESULT" | grep -q '"id"'; then
    echo "Created repo: $TEST_REPO"
else
    echo "Failed to create repo:"
    echo "$CREATE_RESULT"
    exit 1
fi
sleep 3  # Give GitHub a moment

echo ""
echo "=== Step 3: Create test issue ==="
ISSUE_TITLE="E2E Test Issue: Crash on startup"
ISSUE_BODY="This is a test issue created by the ghissues E2E test suite.

## Expected Behavior
- ghissues should mount this repo
- This issue should appear as a markdown file
- Edits should sync back to GitHub

## Test Data
- Created: $(date -u +%Y-%m-%dT%H:%M:%SZ)
- Run ID: ${GITHUB_RUN_ID:-local}

## Marker
E2E_TEST_MARKER_ORIGINAL"

# Escape the body for JSON
ISSUE_BODY_ESCAPED=$(echo "$ISSUE_BODY" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))' 2>/dev/null || echo "$ISSUE_BODY" | sed 's/"/\\"/g' | tr '\n' ' ')

ISSUE_RESULT=$(github_api POST "/repos/$TEST_REPO/issues" "{\"title\":\"$ISSUE_TITLE\",\"body\":$ISSUE_BODY_ESCAPED}")
ISSUE_NUMBER=$(echo "$ISSUE_RESULT" | grep -o '"number": *[0-9]*' | head -1 | grep -o '[0-9]*')

if [ -z "$ISSUE_NUMBER" ]; then
    echo "Failed to create issue:"
    echo "$ISSUE_RESULT"
    exit 1
fi

ISSUE_URL="https://github.com/$TEST_REPO/issues/$ISSUE_NUMBER"
echo "Created issue #${ISSUE_NUMBER}: $ISSUE_URL"

# Wait for issue to be indexed by GitHub API
echo "Waiting for issue to be indexed..."
for i in {1..10}; do
    ISSUE_LIST=$(github_api GET "/repos/$TEST_REPO/issues")
    ISSUE_COUNT=$(echo "$ISSUE_LIST" | grep -c '"number"' || echo "0")
    if [ "$ISSUE_COUNT" -ge 1 ]; then
        echo "Issue indexed after ${i}s"
        break
    fi
    sleep 1
done

echo ""
echo "=== Step 4: Build ghissues ==="
cd "$(dirname "$0")/.."
go build -o ghissues ./cmd/ghissues
echo "Built successfully"

echo ""
echo "=== Step 5: Mount and test ==="
MOUNTPOINT=$(mktemp -d)
CACHE_DIR=$(mktemp -d)
echo "Mountpoint: $MOUNTPOINT"
echo "Cache dir: $CACHE_DIR"

# Start mount in background
echo "Starting mount..."
HOME="$CACHE_DIR" ./ghissues mount "$TEST_REPO" "$MOUNTPOINT" &
MOUNT_PID=$!

# Wait for mount to be ready
sleep 5

# Check if mount is still running
if ! kill -0 $MOUNT_PID 2>/dev/null; then
    echo "ERROR: Mount process died"
    wait $MOUNT_PID || true
    exit 1
fi

echo ""
echo "=== Step 6: Verify read ==="
echo "Listing files..."
ls -la "$MOUNTPOINT/"

# Find the issue file
ISSUE_FILE=$(ls "$MOUNTPOINT/" | grep "\[${ISSUE_NUMBER}\].md" | head -1)
if [ -z "$ISSUE_FILE" ]; then
    echo "ERROR: Could not find issue file for #${ISSUE_NUMBER}"
    ls -la "$MOUNTPOINT/"
    kill $MOUNT_PID 2>/dev/null || true
    exit 1
fi

echo "Found issue file: $ISSUE_FILE"
echo ""
echo "Reading content:"
cat "$MOUNTPOINT/$ISSUE_FILE"

# Verify content
if ! grep -q "E2E_TEST_MARKER_ORIGINAL" "$MOUNTPOINT/$ISSUE_FILE"; then
    echo "ERROR: Original marker not found in file"
    kill $MOUNT_PID 2>/dev/null || true
    exit 1
fi
echo ""
echo "✓ Read test passed"

echo ""
echo "=== Step 7: Test write and sync ==="
# Append our edit marker
echo "" >> "$MOUNTPOINT/$ISSUE_FILE"
echo "## E2E Edit" >> "$MOUNTPOINT/$ISSUE_FILE"
echo "E2E_TEST_MARKER_EDITED_$(date +%s)" >> "$MOUNTPOINT/$ISSUE_FILE"

echo "Edit written, waiting for sync..."
sleep 3  # Wait for debounced sync

echo ""
echo "=== Step 8: Verify sync to GitHub ==="
# Fetch issue from GitHub API directly
ISSUE_DATA=$(github_api GET "/repos/$TEST_REPO/issues/$ISSUE_NUMBER")
REMOTE_BODY=$(echo "$ISSUE_DATA" | python3 -c 'import json,sys; print(json.loads(sys.stdin.read()).get("body",""))' 2>/dev/null || echo "$ISSUE_DATA")

if echo "$REMOTE_BODY" | grep -q "E2E_TEST_MARKER_EDITED"; then
    echo "✓ Sync test passed - edit visible on GitHub"
else
    echo "ERROR: Edit not synced to GitHub"
    echo "Remote body:"
    echo "$REMOTE_BODY"
    kill $MOUNT_PID 2>/dev/null || true
    exit 1
fi

echo ""
echo "=== Step 9: Cleanup mount ==="
# Unmount gracefully
kill -INT $MOUNT_PID 2>/dev/null || true
sleep 2

# Force cleanup if needed
if kill -0 $MOUNT_PID 2>/dev/null; then
    kill -9 $MOUNT_PID 2>/dev/null || true
fi

# Cleanup mountpoint
umount "$MOUNTPOINT" 2>/dev/null || true
rmdir "$MOUNTPOINT" 2>/dev/null || true
rm -rf "$CACHE_DIR" 2>/dev/null || true

echo ""
echo "=========================================="
echo "✓ All E2E tests passed!"
echo "=========================================="
echo ""
echo "Test repo kept for inspection: https://github.com/$TEST_REPO"
echo "Issue: https://github.com/$TEST_REPO/issues/$ISSUE_NUMBER"
echo ""
echo "The repo will be deleted on the next test run."
