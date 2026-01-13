# Walking Skeleton: Task Breakdown

## Phase 1: Foundation

### 1.1 Project Setup
```bash
go mod init github.com/yourorg/ghissues
```
- [x] Create `main.go` with CLI entrypoint
- [x] Create directory structure:
  ```
  /cmd/ghissues/main.go    # CLI entrypoint
  /internal/gh/client.go   # GitHub API client
  /internal/cache/db.go    # SQLite cache
  /internal/fs/fuse.go     # FUSE filesystem
  /internal/sync/engine.go # Sync engine
  /internal/md/format.go   # Markdown formatter
  ```
- [x] Add dependencies:
  - `github.com/hanwen/go-fuse/v2` (FUSE)
  - `modernc.org/sqlite` (pure Go SQLite, no CGO)
  - `github.com/spf13/cobra` (CLI)

### 1.2 CLI Commands
- [x] `ghissues mount <owner/repo> <mountpoint>`
  - Validate repo format (contains `/`)
  - Validate mountpoint exists and is empty
  - Start FUSE mount (foreground for now)
- [x] `ghissues unmount <mountpoint>`
  - Call fusermount to unmount
  - Flush pending syncs

---

## Phase 2: GitHub Integration

### 2.1 Auth Token
- [x] Read `gh` auth token from `~/.config/gh/hosts.yml`
- [x] Parse YAML, extract `oauth_token` for `github.com`
- [x] Fall back to `GITHUB_TOKEN` env var

### 2.2 API Client
- [x] `ListIssues(owner, repo string) ([]Issue, error)`
  - GET `/repos/{owner}/{repo}/issues`
  - Handle pagination (Link header)
  - Return: number, title, body, state, labels, user, created_at, updated_at
- [x] `GetIssue(owner, repo string, number int) (*Issue, etag string, error)`
  - GET `/repos/{owner}/{repo}/issues/{number}`
  - Return etag from response header
- [x] `UpdateIssue(owner, repo string, number int, body string) error`
  - PATCH `/repos/{owner}/{repo}/issues/{number}`
  - Send: `{"body": "..."}`
- [x] `CreateIssue(owner, repo, title, body string, labels []string) (*Issue, error)`
  - POST `/repos/{owner}/{repo}/issues`
- [x] `ListComments(owner, repo string, number int) ([]Comment, error)`
  - GET `/repos/{owner}/{repo}/issues/{number}/comments`
- [x] `CreateComment(owner, repo string, number int, body string) (*Comment, error)`
  - POST `/repos/{owner}/{repo}/issues/{number}/comments`
- [x] `UpdateComment(owner, repo string, commentID int64, body string) error`
  - PATCH `/repos/{owner}/{repo}/issues/comments/{commentID}`

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
- [x] `InitDB(path string) (*DB, error)` — create tables if not exist
- [x] `UpsertIssue(issue Issue) error` — insert or update
- [x] `GetIssue(repo string, number int) (*Issue, error)`
- [x] `ListIssues(repo string) ([]Issue, error)`
- [x] `MarkDirty(repo string, number int, body string) error`
- [x] `GetDirtyIssues(repo string) ([]Issue, error)`
- [x] `ClearDirty(repo string, number int) error`
- [x] Comment operations: `UpsertComment`, `GetComments`, `MarkCommentDirty`, etc.
- [x] Pending tables: `pending_comments`, `pending_issues` for new items

---

## Phase 4: Markdown Format

### 4.1 Issue → Markdown
- [x] Generate frontmatter (YAML between `---`)
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
- [x] Add title as `# Title`
- [x] Add body under `## Body`
- [x] Add comments under `## Comments` with `<!-- comment_id: N -->` markers

### 4.2 Markdown → Issue
- [x] Parse frontmatter to extract `number`, `repo`
- [x] Extract title from `# Title` line
- [x] Extract body from `## Body` section
- [x] Detect what changed (title? body?)
- [x] Parse comments section with `### author` headers
- [x] Detect new comments (`### new`) and edited comments

---

## Phase 5: FUSE Filesystem

### 5.1 Mount Lifecycle
- [x] Create FUSE server with mountpoint
- [x] Implement `OnMount` — trigger initial sync
- [x] Implement `OnUnmount` — flush dirty, close DB
- [x] Handle SIGINT/SIGTERM gracefully

### 5.2 Directory Operations
- [x] `Readdir` — return list of filenames
  - Format: `sanitized-title[number].md`
  - Sanitize: lowercase, replace spaces with `-`, remove special chars
  - Truncate title to reasonable length
- [x] `Create` — create new issue files with `title[new].md` pattern
- [x] `Unlink` — reject deletion (returns EPERM)
- [x] `Rename` — reject rename/move (returns EPERM)

### 5.3 File Operations
- [x] `Lookup` — parse filename to get issue number
- [x] `Getattr` — return file size, mode, times
- [x] `Open` — return file handle
- [x] `Read` — generate markdown from cache, return bytes
- [x] `Write` — buffer writes
- [x] `Flush` — parse markdown, update cache, trigger sync
- [x] Handle comments: detect new/edited comments on flush

---

## Phase 6: Sync Engine

### 6.1 Initial Sync
- [x] On mount: fetch all issues from GitHub
- [x] Upsert each into cache
- [x] Store etag for each issue
- [x] Fetch and cache comments for each issue

### 6.2 Background Refresh
- [x] On file read: check if stale (e.g., >1 min old)
- [x] Fetch with `If-None-Match: {etag}`
- [x] On 304: do nothing
- [x] On 200: update cache (if not dirty)

### 6.3 Write Sync
- [x] On flush: mark issue dirty, record timestamp
- [x] Start/reset debounce timer (e.g., 500ms)
- [x] On timer fire: get all dirty issues
- [x] For each dirty issue:
  - Fetch remote to check `updated_at`
  - If remote `updated_at` > local `local_updated_at`: conflict
    - Keep local (per design decision)
  - Push update to GitHub
  - Clear dirty flag

### 6.4 Comment & Issue Creation Sync (Slice 3)
- [x] Sync order: pending issues → pending comments → dirty comments → dirty issues
- [x] `syncPendingIssues` — create new issues on GitHub, update cache with real numbers
- [x] `syncPendingComments` — create new comments on GitHub
- [x] `syncDirtyComments` — update edited comments on GitHub

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
- [x] Unit tests for markdown parser
- [x] Unit tests for cache operations
- [x] Unit tests for GitHub client
- [x] Unit tests for sync engine
- [x] Unit tests for FUSE operations (newIssueFileNode)
- [x] Integration tests with mock GitHub API (FUSE mount tests)
- [x] E2E tests with real GitHub repo (`scripts/e2e-real-github.sh`)
- [x] GitHub Actions CI: builds, unit tests, integration tests, E2E workflow

---

## Slice 3: Comments & New Issue Creation (COMPLETE)

### Features Implemented
- [x] **Add new comments**: Append `### new` section to issue file, syncs to GitHub
- [x] **Edit existing comments**: Modify comment body, syncs changes to GitHub
- [x] **Create new issues**: Create `title[new].md` file with frontmatter template

### File Format
```markdown
## Comments

### alice
<!-- comment_id: 12345 -->

Existing comment body here...

### new
<!-- comment_id: new -->

New comment to be created...
```

### New Issue Template
```markdown
---
repo: owner/repo
state: open
labels: []
---

# Issue Title

## Body

Issue description here...
```

---

## Definition of Done: Walking Skeleton

The walking skeleton is complete when:

1. ✅ `ghissues mount owner/repo ./mount` works
2. ✅ `ls ./mount/` shows issue files
3. ✅ `cat ./mount/issue[N].md` returns markdown
4. ✅ Editing body and saving syncs to GitHub
5. ✅ `ghissues unmount ./mount` cleanly exits
6. ✅ Works with `gh` CLI authentication
7. ✅ Comments displayed in issue files
8. ✅ New comments can be added by editing files
9. ✅ Existing comments can be edited
10. ✅ New issues can be created via `title[new].md` files
11. ✅ `rm` and `mv` operations are rejected (safety)

---

## Estimated Complexity

| Component | Complexity | Status | Notes |
|-----------|------------|--------|-------|
| CLI | Low | ✅ Done | Cobra makes this easy |
| GitHub Client | Low | ✅ Done | REST API is straightforward |
| SQLite Cache | Low | ✅ Done | Simple CRUD + pending tables |
| Markdown Parser | Medium | ✅ Done | Parsing with comment markers |
| FUSE Filesystem | High | ✅ Done | Most complexity here |
| Sync Engine | Medium | ✅ Done | Debounce + conflict logic |

**Actual order followed:** CLI → GitHub → Cache → Markdown → FUSE → Sync → Comments → New Issues
