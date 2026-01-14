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

Download from [Releases](https://github.com/JohanCodinha/ghissues/releases):

| Platform | Architecture | Binary |
|----------|--------------|--------|
| macOS | Apple Silicon (M1/M2/M3) | `ghissues-darwin-arm64` |
| macOS | Intel | `ghissues-darwin-amd64` |
| Linux | x86_64 | `ghissues-linux-amd64` |
| Linux | ARM64 | `ghissues-linux-arm64` |

```bash
# Example: Install on macOS Apple Silicon
curl -L https://github.com/JohanCodinha/ghissues/releases/latest/download/ghissues-darwin-arm64 -o ghissues
chmod +x ghissues
sudo mv ghissues /usr/local/bin/
```

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
├── .status                    # sync status (read-only)
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
comments: 2
parent_issue: 1200
sub_issues_total: 3
sub_issues_completed: 1
---

# Crash on startup

## Body

Application crashes immediately after login...

## Comments

### 2026-01-10T14:12:00Z - alice
<!-- comment_id: 987654 -->

I can reproduce this on version 2.3.1

### 2026-01-10T16:03:00Z - bob
<!-- comment_id: 987655 -->

Looking into it now.
```

### Editing issues

Edit the `## Body` section and save. Changes sync back to GitHub automatically (debounced 500ms after save).

### Editing state and labels

Change `state: open` to `state: closed` in the frontmatter to close an issue. Modify `labels: [bug, enhancement]` to add or remove labels. Changes sync automatically.

### Adding comments

Add a new comment by appending a `### new` section under `## Comments`:

```markdown
## Comments

### new

My new comment text here.
```

Save the file and the comment will be created on GitHub.

### Editing comments

Edit the text under any existing comment header (e.g., `### 2026-01-10T14:12:00Z - alice`). The `<!-- comment_id: ... -->` tag identifies which comment to update. Changes sync automatically.

### Creating new issues

Create a new file named `your-title[new].md` with the following structure:

```markdown
---
repo: owner/repo
---

# Your Issue Title

## Body

Your issue description here.
```

Save the file and a new issue will be created on GitHub. The file will be renamed to include the assigned issue number.

### Sub-issues (parent-child relationships)

Issues can have parent-child relationships. The frontmatter shows:
- `parent_issue: N` - the parent issue number (if this issue has a parent)
- `sub_issues_total: N` - total number of sub-issues
- `sub_issues_completed: N` - number of completed sub-issues

To set or change a parent issue, edit the `parent_issue` field in the frontmatter. To remove a parent, set it to `0` or remove the line.

## File Format Requirements

ghissues expects a specific markdown structure. Edits that break this structure will fail to save.

### Required Structure

```markdown
---
id: 1234
repo: owner/repo
state: open
labels: [bug, enhancement]
# ... other frontmatter fields
---

# Issue Title

## Body

Your issue content here...

## Comments

### 2026-01-10T14:12:00Z - alice
<!-- comment_id: 987654 -->

Existing comment (editable)

### new

New comment to add
```

### What You Can Safely Edit

- **Title**: Change the `# Title` line
- **Body**: Edit content under `## Body`
- **State**: Change `state: open` to `state: closed` (or vice versa)
- **Labels**: Modify the `labels: [...]` array
- **Parent issue**: Set or change `parent_issue: N`
- **Comments**: Edit existing comment bodies or add `### new` sections

### What Will Cause Errors

- Removing the `---` frontmatter delimiters
- Modifying read-only frontmatter fields (id, repo, url, author, timestamps, etag)
- Malformed YAML in frontmatter (unclosed brackets, invalid types)
- Invalid state values (only `open` or `closed` are valid)

Note: The `# Title` line and `## Body` section are optional for parsing, but removing them will result in empty title/body being saved.

### Error Handling

If you see an "Input/output error" when saving, your changes broke the expected format. The file will revert to its original content on next read.

The canonical template is defined in `internal/md/format.go` (`ToMarkdown` function) and documented in `USER_STORY.md`.

### Unmount

```bash
ghissues unmount ./mountpoint
```

Or press `Ctrl+C` in the terminal running the mount.

### Logging Options

```bash
# Set log level (default: info)
ghissues mount owner/repo ./mount --log-level debug

# Log to file
ghissues mount owner/repo ./mount --log-file /var/log/ghissues.log

# Quiet mode (errors only)
ghissues mount owner/repo ./mount -q

# Combine options
ghissues mount owner/repo ./mount --log-level debug --log-file debug.log
```

Available log levels:
- `debug`: Verbose output including sync details
- `info`: General operational information (default)
- `warn`: Warnings and potential issues
- `error`: Errors only

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
- Pending changes persist across sessions and retry on next mount

### Sync status

A virtual `.status` file in the mountpoint shows current sync state:

```bash
cat ./issues/.status
```

```
# ghissues status

Last sync: 2026-01-14T10:30:00Z
Pending issues: 0
Pending comments: 2
Dirty issues: 1
Dirty comments: 0
Last error: none
```

### Conflict resolution

If an issue is modified on GitHub after you started editing locally:
- **Local wins** if your edit is newer than the remote version
- **Remote wins** if GitHub's version is newer - your local changes are backed up to `~/.cache/ghissues/.conflicts/` before applying the remote version

### Rate limiting

GitHub API rate limits are handled automatically:
- When rate limited, ghissues sleeps until the reset time and retries
- No action required - operations resume automatically

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
│   └── sync/
│       ├── engine.go         # Sync engine
│       └── conflicts.go      # Conflict backup handling
├── scripts/
│   ├── e2e-real-github.sh    # E2E test script
│   └── run-integration-tests.sh
└── .github/workflows/
    ├── test.yml              # CI: unit + integration tests
    ├── e2e.yml               # Manual: real GitHub E2E test
    └── release.yml           # Auto-release on version tags
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

### Test coverage

```bash
# Generate coverage report
go test -coverprofile=coverage.out ./...

# View coverage summary
go tool cover -func=coverage.out

# Generate HTML coverage report
go tool cover -html=coverage.out -o coverage.html
open coverage.html
```

## License

MIT
