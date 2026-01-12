# ghissues

A FUSE filesystem that mounts GitHub issues as markdown files.

## User Story

**As a developer**, I want GitHub issues to appear as local markdown files that I can browse, search, and edit with my existing tools.

## Usage

```bash
# Mount issues to any directory
ghissues mount owner/repo ./issues

# It's just a folder with markdown files
ls ./issues/
# → crash-on-startup[1234].md
# → add-dark-mode[1189].md

# Use any tool
grep -r "authentication" ./issues/
vim ./issues/crash-on-startup[1234].md
cat ./issues/add-dark-mode[1189].md | head -20

# Unmount
ghissues unmount ./issues
```

## File Format

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
comments: 2
etag: "abc123"
---

# Crash on startup

## Body

Application crashes immediately after login when user has more than 100 items in their cart.

Stack trace:
...

## Comments

### 2026-01-10 14:12Z — alice
<!-- comment_id: 987654 -->

I can reproduce this on version 2.3.1

### 2026-01-10 16:03Z — bob
<!-- comment_id: 987655 -->

Looking into it now. Seems related to the pagination changes.
```

## What Success Looks Like

- Files are indistinguishable from local markdown
- Edits sync back to GitHub automatically
- Works offline, syncs when connected
- Single binary, no dependencies
