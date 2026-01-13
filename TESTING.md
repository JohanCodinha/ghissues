# Testing Strategy

This document describes the testing strategy for ghissues, including what each test level covers, how to run tests, and how to add new tests.

## Test Pyramid

```
                    ┌───────────────┐
                    │   E2E Tests   │  ← Manual trigger, real GitHub
                    │   (sparse)    │
                    └───────┬───────┘
                    ┌───────┴───────┐
                    │  Integration  │  ← CI, FUSE in Docker, mock GitHub
                    │    Tests      │
                    └───────┬───────┘
              ┌─────────────┴─────────────┐
              │        Unit Tests         │  ← CI, fast, no external deps
              │        (majority)         │
              └───────────────────────────┘
```

## Test Levels

### 1. Unit Tests

**Location:** `internal/*/\*_test.go`

**What they test:**
- Individual functions in isolation
- Business logic correctness
- Edge cases and error handling

**Run locally:**
```bash
go test ./...

# With verbose output
go test -v ./...

# With race detection
go test -race ./...

# Specific package
go test ./internal/cache/...
```

**CI:** Runs automatically on every push/PR via `.github/workflows/test.yml`

#### Test coverage by package

| Package | Tests | What's covered |
|---------|-------|----------------|
| `internal/cache` | 19 tests | SQLite CRUD, dirty tracking, JSON serialization |
| `internal/fs` | 31 tests | Filename sanitization, parsing, round-trips |
| `internal/gh` | 4 tests | Token retrieval (skipped if no token) |
| `internal/md` | 20 tests | Markdown ↔ Issue conversion, change detection |
| `internal/sync` | 9 tests | Repo parsing, conflict detection, debounce |

### 2. Integration Tests

**Location:** `internal/integration/e2e_test.go`

**What they test:**
- Full FUSE mount → read → write → sync cycle
- Component interaction
- FUSE filesystem behavior

**Key difference from E2E:** Uses a **mock GitHub server** (`gh.NewMockServer()`) instead of real GitHub API.

**Run locally (requires Docker):**
```bash
./scripts/run-integration-tests.sh

# Or manually:
docker build -f Dockerfile.test -t ghissues-integration .
docker run --rm \
    --privileged \
    --device /dev/fuse \
    ghissues-integration
```

**CI:** Runs automatically on every push/PR.

#### What the integration tests verify

1. **TestE2E_MountReadWrite**
   - FUSE mounts successfully
   - `ls` shows files from mock issues
   - `cat` returns correct markdown content
   - File edits trigger sync to mock server
   - Changes appear in mock server state

2. **TestE2E_OfflineMode**
   - Issues are cached on initial sync
   - Reads work after mock server is shut down
   - Cache serves correct content offline

### 3. E2E Tests (Real GitHub)

**Location:** `scripts/e2e-real-github.sh`

**What they test:**
- Full round-trip against actual GitHub API
- Real authentication
- Real network conditions
- Actual sync behavior

**Run locally (requires token with `repo` + `delete_repo` scopes):**
```bash
# Via Docker (recommended if FUSE not installed locally)
./scripts/e2e-docker.sh

# Or directly (requires FUSE installed and working)
GITHUB_TOKEN=$(gh auth token) ./scripts/e2e-real-github.sh
```

**Run in CI (manual trigger):**
```bash
gh workflow run e2e.yml --repo JohanCodinha/ghissues

# Watch the run
gh run watch --repo JohanCodinha/ghissues
```

#### E2E test flow

```
1. Delete test repo (JohanCodinha/ghissues-e2e-test) if exists
2. Create fresh test repo
3. Create test issue with marker text
4. Build ghissues
5. Mount test repo
6. Verify: ls shows issue file
7. Verify: cat shows correct content with marker
8. Edit: append edit marker to file
9. Wait for sync (debounced)
10. Verify: GitHub API shows edit marker in issue body
11. Cleanup: unmount
12. Keep repo alive for inspection
```

#### Test repo inspection

After E2E runs, the test repo stays up for 24 hours:
- **URL:** https://github.com/JohanCodinha/ghissues-e2e-test
- **Issue:** https://github.com/JohanCodinha/ghissues-e2e-test/issues/1

You can:
- View the issue in GitHub UI
- Clone the repo
- Check the edit history
- Debug failures

The repo is deleted at the start of the next E2E run.

## CI Workflows

### `.github/workflows/test.yml`

**Triggers:** Push to main, Pull requests

**Jobs:**
| Job | Duration | What it does |
|-----|----------|--------------|
| `unit-tests` | ~45s | `go test -race ./...` |
| `integration-tests` | ~55s | FUSE tests in Docker with mock GitHub |
| `build` | ~30s | Cross-compile for linux/darwin × amd64/arm64 |

### `.github/workflows/e2e.yml`

**Triggers:** Manual only (`workflow_dispatch`)

**Jobs:**
| Job | Duration | What it does |
|-----|----------|--------------|
| `e2e-real-github` | ~75s | Full test against real GitHub API |

**How to trigger:**
```bash
gh workflow run e2e.yml --repo JohanCodinha/ghissues
```

## Adding New Tests

### Adding unit tests

1. Create `*_test.go` file next to the code being tested
2. Use table-driven tests for multiple cases:

```go
func TestMyFunction(t *testing.T) {
    tests := []struct {
        name     string
        input    string
        expected string
    }{
        {"simple case", "foo", "FOO"},
        {"empty", "", ""},
    }

    for _, tt := range tests {
        t.Run(tt.name, func(t *testing.T) {
            got := MyFunction(tt.input)
            if got != tt.expected {
                t.Errorf("got %q, want %q", got, tt.expected)
            }
        })
    }
}
```

### Adding integration tests

1. Add test functions to `internal/integration/e2e_test.go`
2. Use the build tag `//go:build integration`
3. Use `gh.NewMockServer()` for GitHub API mocking:

```go
func TestMyIntegration(t *testing.T) {
    if os.Getuid() != 0 {
        t.Skip("FUSE tests require root")
    }

    mockGH := gh.NewMockServer()
    defer mockGH.Close()

    mockGH.AddIssue(&gh.Issue{...})

    // ... test FUSE operations
}
```

### Extending E2E tests

Edit `scripts/e2e-real-github.sh` to add new verification steps:

```bash
echo "=== Step N: My new test ==="
# Add your test logic here
if ! some_condition; then
    echo "ERROR: My test failed"
    kill $MOUNT_PID 2>/dev/null || true
    exit 1
fi
echo "✓ My test passed"
```

## Test Dependencies

### Unit tests
- No external dependencies
- Run anywhere with Go installed

### Integration tests
- Docker
- FUSE support in container (`--privileged --device /dev/fuse`)

### E2E tests
- GitHub token with `repo` + `delete_repo` scopes
- Either:
  - Docker (for `./scripts/e2e-docker.sh`)
  - Local FUSE support (for direct script execution)

## Debugging Test Failures

### Unit test failures

```bash
# Run specific test with verbose output
go test -v -run TestMyFunction ./internal/mypackage/

# Run with debug output
go test -v ./... 2>&1 | tee test.log
```

### Integration test failures

```bash
# Run integration tests with verbose output
docker run --rm \
    --privileged \
    --device /dev/fuse \
    ghissues-integration \
    go test -v -tags=integration ./internal/integration/...
```

### E2E test failures

1. Check the test repo: https://github.com/JohanCodinha/ghissues-e2e-test
2. View CI logs: `gh run view <run-id> --log`
3. Run locally with verbose output:
   ```bash
   GITHUB_TOKEN=$(gh auth token) bash -x ./scripts/e2e-real-github.sh
   ```

## Test Secrets

### `GHISSUES_TEST_TOKEN`

Required for E2E tests in CI. Must have scopes:
- `repo` (full control of repositories)
- `delete_repo` (delete test repositories)

**Setup:**
```bash
# Use your existing token (after adding delete_repo scope)
gh auth refresh -h github.com -s delete_repo
gh auth token | gh secret set GHISSUES_TEST_TOKEN --repo JohanCodinha/ghissues

# Or create a dedicated token at:
# https://github.com/settings/tokens/new?scopes=repo,delete_repo
```
