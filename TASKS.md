# Walking Skeleton: Task Breakdown

## Phase 1: Foundation

### 1.1 Project Setup
```bash
go mod init github.com/yourorg/ghissues
```
- [ ] Create `main.go` with CLI entrypoint
- [ ] Create directory structure:
  ```
  /cmd/ghissues/main.go    # CLI entrypoint
  /internal/gh/client.go   # GitHub API client
  /internal/cache/db.go    # SQLite cache
  /internal/fs/fuse.go     # FUSE filesystem
  /internal/sync/engine.go # Sync engine
  /internal/md/format.go   # Markdown formatter
  ```
- [ ] Add dependencies:
  - `github.com/hanwen/go-fuse/v2` (FUSE)
  - `modernc.org/sqlite` (pure Go SQLite, no CGO)
  - `github.com/spf13/cobra` (CLI)

### 1.2 CLI Commands
- [ ] `ghissues mount <owner/repo> <mountpoint>`
  - Validate repo format (contains `/`)
  - Validate mountpoint exists and is empty
  - Start FUSE mount (foreground for now)
- [ ] `ghissues unmount <mountpoint>`
  - Call fusermount to unmount
  - Flush pending syncs

---

## Phase 2: GitHub Integration

### 2.1 Auth Token
- [ ] Read `gh` auth token from `~/.config/gh/hosts.yml`
- [ ] Parse YAML, extract `oauth_token` for `github.com`
- [ ] Fall back to `GITHUB_TOKEN` env var

### 2.2 API Client
- [ ] `ListIssues(owner, repo string) ([]Issue, error)`
  - GET `/repos/{owner}/{repo}/issues`
  - Handle pagination (Link header)
  - Return: number, title, body, state, labels, user, created_at, updated_at
- [ ] `GetIssue(owner, repo string, number int) (*Issue, etag string, error)`
  - GET `/repos/{owner}/{repo}/issues/{number}`
  - Return etag from response header
- [ ] `UpdateIssue(owner, repo string, number int, body string) error`
  - PATCH `/repos/{owner}/{repo}/issues/{number}`
  - Send: `{"body": "..."}`

---

## Phase 3: Cache Layer

### 3.1 Schema
```sql
CREATE TABLE issues (
    id INTEGER PRIMARY KEY,
    number INTEGER NOT NULL,
    repo TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT,
    state TEXT,
    author TEXT,
    created_at TEXT,
    updated_at TEXT,
    etag TEXT,
    dirty INTEGER DEFAULT 0,
    local_updated_at TEXT,
    UNIQUE(repo, number)
);
```

### 3.2 Operations
- [ ] `InitDB(path string) (*DB, error)` — create tables if not exist
- [ ] `UpsertIssue(issue Issue) error` — insert or update
- [ ] `GetIssue(repo string, number int) (*Issue, error)`
- [ ] `ListIssues(repo string) ([]Issue, error)`
- [ ] `MarkDirty(repo string, number int, body string) error`
- [ ] `GetDirtyIssues(repo string) ([]Issue, error)`
- [ ] `ClearDirty(repo string, number int) error`

---

## Phase 4: Markdown Format

### 4.1 Issue → Markdown
- [ ] Generate frontmatter (YAML between `---`)
  ```yaml
  id: 1234
  repo: owner/repo
  url: https://github.com/owner/repo/issues/1234
  state: open
  author: alice
  created_at: 2026-01-08T09:15:00Z
  updated_at: 2026-01-10T16:03:00Z
  etag: "abc123"
  ```
- [ ] Add title as `# Title`
- [ ] Add body under `## Body`
- [ ] (Slice 2: Add comments under `## Comments`)

### 4.2 Markdown → Issue
- [ ] Parse frontmatter to extract `number`, `repo`
- [ ] Extract title from `# Title` line
- [ ] Extract body from `## Body` section
- [ ] Detect what changed (title? body?)

---

## Phase 5: FUSE Filesystem

### 5.1 Mount Lifecycle
- [ ] Create FUSE server with mountpoint
- [ ] Implement `OnMount` — trigger initial sync
- [ ] Implement `OnUnmount` — flush dirty, close DB
- [ ] Handle SIGINT/SIGTERM gracefully

### 5.2 Directory Operations
- [ ] `Readdir` — return list of filenames
  - Format: `sanitized-title[number].md`
  - Sanitize: lowercase, replace spaces with `-`, remove special chars
  - Truncate title to reasonable length

### 5.3 File Operations
- [ ] `Lookup` — parse filename to get issue number
- [ ] `Getattr` — return file size, mode, times
- [ ] `Open` — return file handle
- [ ] `Read` — generate markdown from cache, return bytes
- [ ] `Write` — buffer writes
- [ ] `Flush` — parse markdown, update cache, trigger sync

---

## Phase 6: Sync Engine

### 6.1 Initial Sync
- [ ] On mount: fetch all issues from GitHub
- [ ] Upsert each into cache
- [ ] Store etag for each issue

### 6.2 Background Refresh
- [ ] On file read: check if stale (e.g., >1 min old)
- [ ] Fetch with `If-None-Match: {etag}`
- [ ] On 304: do nothing
- [ ] On 200: update cache (if not dirty)

### 6.3 Write Sync
- [ ] On flush: mark issue dirty, record timestamp
- [ ] Start/reset debounce timer (e.g., 500ms)
- [ ] On timer fire: get all dirty issues
- [ ] For each dirty issue:
  - Fetch remote to check `updated_at`
  - If remote `updated_at` > local `local_updated_at`: conflict
    - Keep local (per design decision)
  - Push update to GitHub
  - Clear dirty flag

---

## Phase 7: Integration Testing

### 7.1 Manual Test Script
```bash
# 1. Mount
./ghissues mount owner/repo ./test-mount

# 2. List
ls ./test-mount/

# 3. Read
cat "./test-mount/some-issue[123].md"

# 4. Edit
echo "Updated body" >> "./test-mount/some-issue[123].md"

# 5. Wait for sync (check GitHub)

# 6. Unmount
./ghissues unmount ./test-mount
```

### 7.2 Automated Tests
- [ ] Unit tests for markdown parser
- [ ] Unit tests for cache operations
- [ ] Integration test with mock GitHub API
- [ ] (Later) E2E test with real GitHub repo

---

## Definition of Done: Walking Skeleton

The walking skeleton is complete when:

1. ✅ `ghissues mount owner/repo ./mount` works
2. ✅ `ls ./mount/` shows issue files
3. ✅ `cat ./mount/issue[N].md` returns markdown
4. ✅ Editing body and saving syncs to GitHub
5. ✅ `ghissues unmount ./mount` cleanly exits
6. ✅ Works with `gh` CLI authentication

---

## Estimated Complexity

| Component | Complexity | Notes |
|-----------|------------|-------|
| CLI | Low | Cobra makes this easy |
| GitHub Client | Low | REST API is straightforward |
| SQLite Cache | Low | Simple CRUD |
| Markdown Parser | Medium | Parsing is fiddly |
| FUSE Filesystem | High | Most complexity here |
| Sync Engine | Medium | Debounce + conflict logic |

**Suggested order:** CLI → GitHub → Cache → Markdown → FUSE → Sync
