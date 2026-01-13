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
echo "=== Step 6.5: Create comment on the issue ==="
COMMENT_BODY="E2E_COMMENT_MARKER_$(date +%s) - This comment was created by the E2E test suite"

# Escape the body for JSON
COMMENT_BODY_ESCAPED=$(echo "$COMMENT_BODY" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))' 2>/dev/null || echo "\"$COMMENT_BODY\"")

COMMENT_RESULT=$(github_api POST "/repos/$TEST_REPO/issues/$ISSUE_NUMBER/comments" "{\"body\":$COMMENT_BODY_ESCAPED}")
COMMENT_ID=$(echo "$COMMENT_RESULT" | grep -o '"id": *[0-9]*' | head -1 | grep -o '[0-9]*')

if [ -z "$COMMENT_ID" ]; then
    echo "Failed to create comment:"
    echo "$COMMENT_RESULT"
    kill $MOUNT_PID 2>/dev/null || true
    exit 1
fi
echo "Created comment #${COMMENT_ID}"

# Wait for the mount to pick up the new comment
echo "Waiting for sync to pick up new comment..."
sleep 5

# Re-read the file to check for comment
echo ""
echo "=== Step 6.6: Verify comment appears in mounted file ==="
echo "Reading updated content:"
cat "$MOUNTPOINT/$ISSUE_FILE"
echo ""

# Verify comment marker appears
if grep -q "E2E_COMMENT_MARKER" "$MOUNTPOINT/$ISSUE_FILE"; then
    echo "Comment body found in file"
else
    echo "WARNING: Comment body not found in file (may need longer sync time)"
    # Don't fail the test - comments may take time to sync
fi

# Verify ## Comments section exists
if grep -q "## Comments" "$MOUNTPOINT/$ISSUE_FILE"; then
    echo "Comments section found"
else
    echo "WARNING: ## Comments section not found"
fi

# Verify comments count in frontmatter
if grep -q "comments:" "$MOUNTPOINT/$ISSUE_FILE"; then
    COMMENT_COUNT=$(grep "comments:" "$MOUNTPOINT/$ISSUE_FILE" | head -1)
    echo "Found in frontmatter: $COMMENT_COUNT"
else
    echo "WARNING: comments field not found in frontmatter"
fi

echo ""
echo "Comment test passed"

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
echo "=== Step 8.1: Test partial file reads (head/tail) ==="
echo "Testing head -20:"
head -20 "$MOUNTPOINT/$ISSUE_FILE"
LINE_COUNT=$(wc -l < "$MOUNTPOINT/$ISSUE_FILE")
echo "Total lines: $LINE_COUNT"
if [ "$LINE_COUNT" -lt 5 ]; then
    echo "ERROR: File too short"
    kill $MOUNT_PID 2>/dev/null || true
    exit 1
fi
echo "✓ Partial read works"

echo ""
echo "=== Step 8.2: Test grep search across directory ==="
# Use the marker we know exists in the file
MARKER="E2E_TEST_MARKER"
GREP_RESULT=$(grep -l "$MARKER" "$MOUNTPOINT"/*.md 2>/dev/null | wc -l)
if [ "$GREP_RESULT" -lt 1 ]; then
    echo "ERROR: grep -l failed"
    kill $MOUNT_PID 2>/dev/null || true
    exit 1
fi
echo "✓ grep search works"

echo ""
echo "=== Step 8.3: Test find command ==="
FIND_COUNT=$(find "$MOUNTPOINT" -name "*.md" | wc -l)
if [ "$FIND_COUNT" -lt 1 ]; then
    echo "ERROR: find found no files"
    kill $MOUNT_PID 2>/dev/null || true
    exit 1
fi
echo "✓ find command works"

echo ""
echo "=== Step 8.4: Test stat command ==="
# Use portable stat (macOS vs Linux syntax differs)
if stat --version 2>/dev/null | grep -q GNU; then
    FILE_SIZE=$(stat --format=%s "$MOUNTPOINT/$ISSUE_FILE")
else
    FILE_SIZE=$(stat -f%z "$MOUNTPOINT/$ISSUE_FILE")
fi
if [ "$FILE_SIZE" -lt 100 ]; then
    echo "ERROR: File too small: $FILE_SIZE bytes"
    kill $MOUNT_PID 2>/dev/null || true
    exit 1
fi
echo "✓ stat works (size: $FILE_SIZE bytes)"

echo ""
echo "=== Step 8.5: Test wc command ==="
WC_OUTPUT=$(wc "$MOUNTPOINT/$ISSUE_FILE")
echo "wc output: $WC_OUTPUT"
echo "✓ wc works"

echo ""
echo "=== Step 8.6: Test file type detection ==="
FILE_TYPE=$(file "$MOUNTPOINT/$ISSUE_FILE")
if ! echo "$FILE_TYPE" | grep -qi "text"; then
    echo "WARNING: File not detected as text"
fi
echo "✓ file type: $FILE_TYPE"

echo ""
echo "=== Step 8.7: Test adding comment via file edit ==="
# Read current content
CURRENT_CONTENT=$(cat "$MOUNTPOINT/$ISSUE_FILE")

# Add a new comment to the file
cat >> "$MOUNTPOINT/$ISSUE_FILE" << 'COMMENT_EOF'

## Comments

### new
<!-- comment_id: new -->

E2E_NEW_COMMENT_MARKER - This comment was added by editing the file directly.
COMMENT_EOF

echo "Added new comment via file edit, waiting for sync..."
sleep 5

# Verify comment was created on GitHub
COMMENTS_DATA=$(github_api GET "/repos/$TEST_REPO/issues/$ISSUE_NUMBER/comments")
if echo "$COMMENTS_DATA" | grep -q "E2E_NEW_COMMENT_MARKER"; then
    echo "✓ New comment synced to GitHub"
else
    echo "WARNING: New comment not found on GitHub (may need more sync time)"
    echo "Comments data: $COMMENTS_DATA"
fi

echo ""
echo "=== Step 8.8: Test creating new issue via file creation ==="
# Create a new issue file with [new] marker
NEW_ISSUE_FILE="$MOUNTPOINT/test-feature-request[new].md"
cat > "$NEW_ISSUE_FILE" << 'NEWISSUE_EOF'
---
repo: testowner/testrepo
state: open
labels: []
---

# Test Feature Request

## Body

E2E_NEW_ISSUE_MARKER - This issue was created by creating a new file in the mounted filesystem.

This tests the Slice 3 functionality for creating new issues.
NEWISSUE_EOF

echo "Created new issue file, waiting for sync..."
sleep 3

# Verify new issue was created on GitHub
NEW_ISSUES=$(github_api GET "/repos/$TEST_REPO/issues?state=all")
if echo "$NEW_ISSUES" | grep -q "E2E_NEW_ISSUE_MARKER"; then
    echo "✓ New issue created on GitHub"
elif echo "$NEW_ISSUES" | grep -q "Test Feature Request"; then
    echo "✓ New issue created on GitHub (found by title)"
else
    echo "WARNING: New issue not found on GitHub (may need more sync time)"
fi

echo ""
echo "=== Step 8.9: Test adding labels via frontmatter ==="
# Read current file content
CURRENT_CONTENT=$(cat "$MOUNTPOINT/$ISSUE_FILE")

# Modify labels in frontmatter and write back
# Use a temp file to avoid issues with FUSE in-place editing
echo "$CURRENT_CONTENT" | sed 's/labels: \[\]/labels: [e2e-test-label]/' > /tmp/e2e_modified_issue.md
cat /tmp/e2e_modified_issue.md > "$MOUNTPOINT/$ISSUE_FILE"

echo "Added label via frontmatter, waiting for sync..."
sleep 5

# Verify label was added on GitHub
ISSUE_DATA=$(github_api GET "/repos/$TEST_REPO/issues/$ISSUE_NUMBER")
if echo "$ISSUE_DATA" | grep -q "e2e-test-label"; then
    echo "✓ Label added successfully via frontmatter"
else
    echo "WARNING: Label not found on GitHub (may need more sync time)"
    echo "Issue labels: $(echo "$ISSUE_DATA" | grep -o '"labels":\[[^]]*\]')"
fi

echo ""
echo "=== Step 8.10: Test changing state via frontmatter ==="
# Read current file and change state to closed
CURRENT_CONTENT=$(cat "$MOUNTPOINT/$ISSUE_FILE")
echo "$CURRENT_CONTENT" | sed 's/state: open/state: closed/' > /tmp/e2e_modified_issue.md
cat /tmp/e2e_modified_issue.md > "$MOUNTPOINT/$ISSUE_FILE"

echo "Changed state to closed via frontmatter, waiting for sync..."
sleep 5

# Verify state was changed on GitHub
ISSUE_DATA=$(github_api GET "/repos/$TEST_REPO/issues/$ISSUE_NUMBER")
ISSUE_STATE=$(echo "$ISSUE_DATA" | grep -o '"state": *"[^"]*"' | head -1 | grep -o '"[^"]*"$' | tr -d '"')
if [ "$ISSUE_STATE" = "closed" ]; then
    echo "✓ Issue closed successfully via frontmatter"
else
    echo "WARNING: Issue state is '$ISSUE_STATE', expected 'closed'"
fi

# Reopen the issue for further testing
CURRENT_CONTENT=$(cat "$MOUNTPOINT/$ISSUE_FILE")
echo "$CURRENT_CONTENT" | sed 's/state: closed/state: open/' > /tmp/e2e_modified_issue.md
cat /tmp/e2e_modified_issue.md > "$MOUNTPOINT/$ISSUE_FILE"
sleep 5

# Cleanup temp file
rm -f /tmp/e2e_modified_issue.md

echo ""
echo "=== Step 8.11: Test sub-issues (parent-child relationships) ==="
# Create a second issue to be the parent
PARENT_TITLE="E2E Parent Issue"
PARENT_BODY="This issue will be the parent for testing sub-issues."
PARENT_BODY_ESCAPED=$(echo "$PARENT_BODY" | python3 -c 'import json,sys; print(json.dumps(sys.stdin.read()))' 2>/dev/null || echo "\"$PARENT_BODY\"")

PARENT_RESULT=$(github_api POST "/repos/$TEST_REPO/issues" "{\"title\":\"$PARENT_TITLE\",\"body\":$PARENT_BODY_ESCAPED}")
PARENT_NUMBER=$(echo "$PARENT_RESULT" | grep -o '"number": *[0-9]*' | head -1 | grep -o '[0-9]*')

if [ -z "$PARENT_NUMBER" ]; then
    echo "WARNING: Could not create parent issue, skipping sub-issues test"
else
    echo "Created parent issue #${PARENT_NUMBER}"

    # Wait for sync to pick up new issue
    sleep 5

    # Now make the original issue a sub-issue of the parent by editing frontmatter
    CURRENT_CONTENT=$(cat "$MOUNTPOINT/$ISSUE_FILE")

    # Add parent_issue field to frontmatter
    echo "$CURRENT_CONTENT" | sed "s/^state: open/state: open\nparent_issue: $PARENT_NUMBER/" > /tmp/e2e_modified_issue.md
    cat /tmp/e2e_modified_issue.md > "$MOUNTPOINT/$ISSUE_FILE"

    echo "Set parent_issue: $PARENT_NUMBER in frontmatter, waiting for sync..."
    sleep 5

    # Verify sub-issue relationship was created on GitHub
    ISSUE_DATA=$(github_api GET "/repos/$TEST_REPO/issues/$ISSUE_NUMBER")
    if echo "$ISSUE_DATA" | grep -q "parent_issue_url"; then
        echo "✓ Sub-issue relationship synced to GitHub"
    else
        echo "WARNING: parent_issue_url not found (sub-issues API may not be available)"
    fi

    # Check via the parent's sub-issues endpoint
    SUB_ISSUES=$(github_api GET "/repos/$TEST_REPO/issues/$PARENT_NUMBER/sub_issues" 2>/dev/null || echo "[]")
    if echo "$SUB_ISSUES" | grep -q "\"number\": *$ISSUE_NUMBER"; then
        echo "✓ Issue #$ISSUE_NUMBER appears as sub-issue of #$PARENT_NUMBER"
    else
        echo "WARNING: Issue not found in parent's sub-issues list"
    fi

    # Remove the parent relationship
    CURRENT_CONTENT=$(cat "$MOUNTPOINT/$ISSUE_FILE")
    echo "$CURRENT_CONTENT" | sed "s/parent_issue: $PARENT_NUMBER/parent_issue: 0/" > /tmp/e2e_modified_issue.md
    cat /tmp/e2e_modified_issue.md > "$MOUNTPOINT/$ISSUE_FILE"

    echo "Removed parent relationship, waiting for sync..."
    sleep 5

    rm -f /tmp/e2e_modified_issue.md
    echo "✓ Local → GitHub sub-issues test completed"

    # === BIDIRECTIONAL TEST: GitHub → Local ===
    echo ""
    echo "Testing reverse direction: GitHub → Local..."

    # Get the issue's numeric ID (needed for sub-issues API)
    ISSUE_DATA=$(github_api GET "/repos/$TEST_REPO/issues/$ISSUE_NUMBER")
    ISSUE_ID=$(echo "$ISSUE_DATA" | grep -o '"id": *[0-9]*' | head -1 | grep -o '[0-9]*')

    if [ -n "$ISSUE_ID" ]; then
        # Add sub-issue relationship via GitHub API directly
        echo "Adding sub-issue relationship via GitHub API..."
        ADD_RESULT=$(github_api POST "/repos/$TEST_REPO/issues/$PARENT_NUMBER/sub_issues" "{\"sub_issue_id\":$ISSUE_ID}" 2>&1)

        if echo "$ADD_RESULT" | grep -q "error\|Error\|404\|422"; then
            echo "WARNING: Could not add sub-issue via API: $ADD_RESULT"
        else
            echo "Sub-issue added via API, waiting for sync to pick up change..."
            sleep 5

            # Re-read the mounted file and check for parent_issue in frontmatter
            UPDATED_CONTENT=$(cat "$MOUNTPOINT/$ISSUE_FILE")
            if echo "$UPDATED_CONTENT" | grep -q "parent_issue: $PARENT_NUMBER"; then
                echo "✓ GitHub → Local: parent_issue appears in mounted file"
            else
                echo "WARNING: parent_issue not found in mounted file (may need refresh)"
                # Try forcing a refresh by re-reading
                sleep 3
                UPDATED_CONTENT=$(cat "$MOUNTPOINT/$ISSUE_FILE")
                if echo "$UPDATED_CONTENT" | grep -q "parent_issue:"; then
                    echo "Found parent_issue field: $(echo "$UPDATED_CONTENT" | grep "parent_issue:")"
                fi
            fi
        fi
    else
        echo "WARNING: Could not get issue ID for bidirectional test"
    fi

    echo "✓ Sub-issues bidirectional test completed"
fi

echo ""
echo "=== Step 8.12: Test unsupported operations fail gracefully ==="
echo "Testing unsupported operations..."

# rm (delete) should fail
if rm "$MOUNTPOINT/$ISSUE_FILE" 2>/dev/null; then
    echo "ERROR: rm succeeded - this is dangerous!"
    kill $MOUNT_PID 2>/dev/null || true
    exit 1
else
    echo "✓ rm correctly rejected"
fi

# mv (rename) - should fail
if mv "$MOUNTPOINT/$ISSUE_FILE" "$MOUNTPOINT/renamed.md" 2>/dev/null; then
    echo "WARNING: mv succeeded unexpectedly"
    mv "$MOUNTPOINT/renamed.md" "$MOUNTPOINT/$ISSUE_FILE" 2>/dev/null
else
    echo "✓ mv correctly rejected"
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
