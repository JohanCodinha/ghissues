# ghissues

A FUSE filesystem that mounts GitHub issues as markdown files.

## Overview

ghissues lets you browse, search, and edit GitHub issues using your existing tools. Issues appear as local markdown files that you can open in any editor, grep through, or process with scripts.

```bash
# Mount a repository's issues
ghissues mount owner/repo ./issues

# Use any tool
ls ./issues/
cat "./issues/crash-on-startup[1234].md"
grep -r "authentication" ./issues/
vim "./issues/add-dark-mode[1189].md"

# Unmount when done
ghissues unmount ./issues
```

## Installation

### Prerequisites

- **Go 1.21+**
- **FUSE support:**
  - macOS: `brew install macfuse` (requires reboot)
  - Linux: `sudo apt-get install fuse3`
- **GitHub CLI** (for authentication): `brew install gh` or https://cli.github.com

### Build from source

```bash
git clone https://github.com/JohanCodinha/ghissues.git
cd ghissues
go build -o ghissues ./cmd/ghissues
```

### Pre-built binaries

Download from [Releases](https://github.com/JohanCodinha/ghissues/releases) (linux/darwin, amd64/arm64).

## Usage

### Authentication

ghissues uses your GitHub CLI authentication:

```bash
gh auth login
```

Alternatively, set `GITHUB_TOKEN` environment variable.

### Mount a repository

```bash
ghissues mount owner/repo ./mountpoint
```

The mountpoint directory will be created if it doesn't exist.

### File format

Each issue appears as `title[number].md`:

```
./issues/
├── crash-on-startup[1234].md
├── add-dark-mode[1189].md
└── fix-login-bug[1190].md
```

File contents include YAML frontmatter with metadata:

```markdown
---
id: 1234
repo: owner/repo
url: https://github.com/owner/repo/issues/1234
state: open
labels: [bug, p1]
author: alice
created_at: 2026-01-08T09:15:00Z
updated_at: 2026-01-10T16:03:00Z
etag: "abc123"
---

# Crash on startup

## Body

Application crashes immediately after login...
```

### Editing issues

Edit the `## Body` section and save. Changes sync back to GitHub automatically (debounced 500ms after save).

### Unmount

```bash
ghissues unmount ./mountpoint
```

Or press `Ctrl+C` in the terminal running the mount.

## How it works

```
┌─────────────┐     ┌─────────────┐     ┌─────────────┐
│   Your      │────▶│   FUSE      │────▶│   SQLite    │
│   Editor    │     │   Filesystem│     │   Cache     │
└─────────────┘     └─────────────┘     └──────┬──────┘
                                               │
                    ┌─────────────┐             │
                    │ Sync Engine │◀────────────┘
                    │  (debounced)│
                    └──────┬──────┘
                           │
                    ┌──────▼──────┐
                    │ GitHub API  │
                    └─────────────┘
```

1. **On mount:** All issues are fetched and cached locally
2. **On read:** Content is served from cache, background refresh triggered
3. **On write:** Changes are cached, marked dirty, sync scheduled
4. **On sync:** Dirty issues are pushed to GitHub (with conflict detection)

### Caching

- Cache location: `~/.cache/ghissues/owner_repo.db`
- Uses SQLite for reliability
- Supports offline reads from cache

### Conflict resolution

If an issue is modified on GitHub after you started editing locally:
- **Local wins** if your edit is newer
- **Remote wins** if GitHub's version is newer (your changes stay dirty for manual resolution)

## Development

See [TESTING.md](TESTING.md) for testing strategy and instructions.

### Project structure

```
ghissues/
├── cmd/ghissues/main.go      # CLI entrypoint
├── internal/
│   ├── cache/db.go           # SQLite cache layer
│   ├── fs/fuse.go            # FUSE filesystem
│   ├── gh/client.go          # GitHub API client
│   ├── md/format.go          # Markdown formatter
│   └── sync/engine.go        # Sync engine
├── scripts/
│   ├── e2e-real-github.sh    # E2E test script
│   └── run-integration-tests.sh
└── .github/workflows/
    ├── test.yml              # CI: unit + integration tests
    └── e2e.yml               # Manual: real GitHub E2E test
```

### Building

```bash
go build -o ghissues ./cmd/ghissues
```

### Running tests

```bash
# Unit tests
go test ./...

# Integration tests (requires Docker)
./scripts/run-integration-tests.sh

# E2E against real GitHub (requires token with repo+delete_repo scopes)
./scripts/e2e-docker.sh
```

## Roadmap

- [x] **Slice 1:** Walking skeleton (mount, read, edit, sync)
- [ ] **Slice 2:** Comments rendering
- [ ] **Slice 3:** Edit title, add comments, create issues
- [ ] **Slice 4:** Robust offline mode, rate limiting
- [ ] **Slice 5:** Pagination, filtering, real-time updates

## License

MIT
