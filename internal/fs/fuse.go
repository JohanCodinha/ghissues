// Package fs provides a FUSE filesystem for GitHub issues.
package fs

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"regexp"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/JohanCodinha/ghissues/internal/cache"
	"github.com/JohanCodinha/ghissues/internal/logger"
	"github.com/JohanCodinha/ghissues/internal/md"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

const (
	// maxTitleLength is the maximum length for sanitized titles in filenames.
	maxTitleLength = 50
	// maxFileSize is the maximum allowed file size (10MB) to prevent unbounded memory growth.
	maxFileSize = 10 * 1024 * 1024
)

// parseIssueTime parses an RFC3339 timestamp string and returns the time.
// If the timestamp is empty or invalid, it returns the current time as a fallback.
func parseIssueTime(timestamp string) time.Time {
	if timestamp == "" {
		return time.Now()
	}
	t, err := time.Parse(time.RFC3339, timestamp)
	if err != nil {
		return time.Now()
	}
	return t
}

// filenameRegex matches issue filenames in the format: title[number].md
var filenameRegex = regexp.MustCompile(`^(.+)\[(\d+)\]\.md$`)

// newIssueFilenameRegex matches new issue filenames in the format: title[new].md
var newIssueFilenameRegex = regexp.MustCompile(`^(.+)\[new\]\.md$`)

// sanitizeTitle converts an issue title to a filesystem-safe filename component.
// It lowercases, replaces spaces with dashes, removes special characters,
// and truncates to maxTitleLength characters.
func sanitizeTitle(title string) string {
	// Lowercase
	result := strings.ToLower(title)

	// Replace spaces with dashes
	result = strings.ReplaceAll(result, " ", "-")

	// Remove special characters (keep only alphanumeric and dashes)
	var sb strings.Builder
	for _, r := range result {
		if (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9') || r == '-' {
			sb.WriteRune(r)
		}
	}
	result = sb.String()

	// Collapse multiple dashes into one
	for strings.Contains(result, "--") {
		result = strings.ReplaceAll(result, "--", "-")
	}

	// Trim leading/trailing dashes
	result = strings.Trim(result, "-")

	// Truncate to maxTitleLength
	if len(result) > maxTitleLength {
		result = result[:maxTitleLength]
		// Trim trailing dash if truncation left one
		result = strings.TrimSuffix(result, "-")
	}

	// Handle empty result
	if result == "" {
		result = "issue"
	}

	return result
}

// makeFilename creates a filename from an issue title and number.
// Format: sanitized-title[number].md
func makeFilename(title string, number int) string {
	sanitized := sanitizeTitle(title)
	return fmt.Sprintf("%s[%d].md", sanitized, number)
}

// parseFilename extracts the issue number from a filename.
// Returns the issue number and true if successful, or 0 and false if the filename doesn't match.
func parseFilename(name string) (int, bool) {
	matches := filenameRegex.FindStringSubmatch(name)
	if matches == nil || len(matches) < 3 {
		return 0, false
	}

	var number int
	_, err := fmt.Sscanf(matches[2], "%d", &number)
	if err != nil {
		return 0, false
	}

	return number, true
}

// parseNewIssueFilename checks if a filename is for a new issue (title[new].md format).
// Returns the title portion and true if it matches, or empty string and false if not.
func parseNewIssueFilename(name string) (string, bool) {
	matches := newIssueFilenameRegex.FindStringSubmatch(name)
	if matches == nil || len(matches) < 2 {
		return "", false
	}
	return matches[1], true
}

// unsanitizeTitle converts a sanitized filename back to a human-readable title.
// It replaces dashes with spaces and capitalizes each word.
func unsanitizeTitle(sanitized string) string {
	// Replace dashes with spaces
	title := strings.ReplaceAll(sanitized, "-", " ")
	// Capitalize first letter of each word
	words := strings.Fields(title)
	for i, word := range words {
		if len(word) > 0 {
			words[i] = strings.ToUpper(string(word[0])) + word[1:]
		}
	}
	return strings.Join(words, " ")
}

// FS represents the FUSE filesystem for GitHub issues.
type FS struct {
	cache      *cache.DB
	repo       string
	mountpoint string
	server     *fuse.Server
	onDirty    func() // called when an issue is marked dirty
}

// NewFS creates a new FUSE filesystem instance.
// The onDirty callback is called whenever an issue is marked dirty in the cache.
// Pass nil if no callback is needed.
func NewFS(cacheDB *cache.DB, repo, mountpoint string, onDirty func()) *FS {
	return &FS{
		cache:      cacheDB,
		repo:       repo,
		mountpoint: mountpoint,
		onDirty:    onDirty,
	}
}

// Mount starts the FUSE server and blocks until unmounted.
// It sets up signal handlers for graceful shutdown on SIGINT/SIGTERM.
func (f *FS) Mount() error {
	// Create the root node
	root := &rootNode{
		cache:   f.cache,
		repo:    f.repo,
		onDirty: f.onDirty,
	}

	// Create FUSE server options
	opts := &fs.Options{
		MountOptions: fuse.MountOptions{
			Debug:      false,
			FsName:     "ghissues",
			Name:       "ghissues",
			AllowOther: false,
		},
		// Set UID and GID to current user
		UID: uint32(os.Getuid()),
		GID: uint32(os.Getgid()),
	}

	// Mount the filesystem
	server, err := fs.Mount(f.mountpoint, root, opts)
	if err != nil {
		return fmt.Errorf("failed to mount FUSE filesystem: %w", err)
	}
	f.server = server

	// Set up signal handler for graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGINT, syscall.SIGTERM)

	// Handle signals in a goroutine
	go func() {
		<-sigChan
		f.Unmount()
	}()

	// Wait until the server is unmounted
	server.Wait()

	return nil
}

// Unmount stops the FUSE server gracefully.
func (f *FS) Unmount() error {
	if f.server != nil {
		return f.server.Unmount()
	}
	return nil
}

// rootNode represents the root directory of the filesystem.
// It implements fs.InodeEmbedder and fs.NodeReaddirer.
type rootNode struct {
	fs.Inode
	cache   *cache.DB
	repo    string
	onDirty func()
}

var _ = (fs.NodeReaddirer)((*rootNode)(nil))
var _ = (fs.NodeLookuper)((*rootNode)(nil))
var _ = (fs.NodeCreater)((*rootNode)(nil))
var _ = (fs.NodeUnlinker)((*rootNode)(nil))
var _ = (fs.NodeRenamer)((*rootNode)(nil))

// Unlink rejects file deletion - issues cannot be deleted via the filesystem.
func (r *rootNode) Unlink(ctx context.Context, name string) syscall.Errno {
	return syscall.EPERM
}

// Rename rejects file renaming - issues cannot be renamed via the filesystem.
func (r *rootNode) Rename(ctx context.Context, name string, newParent fs.InodeEmbedder, newName string, flags uint32) syscall.Errno {
	return syscall.EPERM
}

// Readdir returns the list of issue files in the directory.
func (r *rootNode) Readdir(ctx context.Context) (fs.DirStream, syscall.Errno) {
	issues, err := r.cache.ListIssues(r.repo)
	if err != nil {
		return nil, syscall.EIO
	}

	entries := make([]fuse.DirEntry, 0, len(issues))
	for _, issue := range issues {
		filename := makeFilename(issue.Title, issue.Number)
		entries = append(entries, fuse.DirEntry{
			Name: filename,
			Ino:  uint64(issue.Number),
			Mode: fuse.S_IFREG,
		})
	}

	return fs.NewListDirStream(entries), 0
}

// Lookup finds a file by name and returns its inode.
func (r *rootNode) Lookup(ctx context.Context, name string, out *fuse.EntryOut) (*fs.Inode, syscall.Errno) {
	// Parse the filename to get the issue number
	number, ok := parseFilename(name)
	if !ok {
		return nil, syscall.ENOENT
	}

	// Get the issue from the cache
	issue, err := r.cache.GetIssue(r.repo, number)
	if err != nil || issue == nil {
		return nil, syscall.ENOENT
	}

	// Get comments from the cache
	comments, err := r.cache.GetComments(r.repo, number)
	if err != nil {
		comments = []cache.Comment{} // Use empty slice on error
	}

	// Generate markdown content to get file size
	content := md.ToMarkdown(issue, comments)

	// Set up attributes
	out.Mode = 0644
	out.Size = uint64(len(content))
	out.Ino = uint64(issue.Number)

	// Set times from issue timestamps
	mtime := parseIssueTime(issue.UpdatedAt)
	ctime := parseIssueTime(issue.CreatedAt)
	out.SetTimes(&mtime, &mtime, &ctime)

	// Create the file node
	fileNode := &issueFileNode{
		cache:   r.cache,
		repo:    r.repo,
		number:  issue.Number,
		onDirty: r.onDirty,
	}

	// Create a stable inode using the issue number
	stable := fs.StableAttr{
		Mode: fuse.S_IFREG,
		Ino:  uint64(issue.Number),
	}

	child := r.NewInode(ctx, fileNode, stable)
	return child, 0
}

// Create creates a new file for a new issue.
// The filename must be in the format: title[new].md
func (r *rootNode) Create(ctx context.Context, name string, flags uint32, mode uint32, out *fuse.EntryOut) (*fs.Inode, fs.FileHandle, uint32, syscall.Errno) {
	// Check if this is a new issue file
	titlePart, ok := parseNewIssueFilename(name)
	if !ok {
		// Not a valid new issue filename
		return nil, nil, 0, syscall.EINVAL
	}

	// Convert sanitized title back to a readable title
	title := unsanitizeTitle(titlePart)

	// Create a new issue file node
	// We use a negative number to indicate a pending issue (not yet created on GitHub)
	pendingID := -int(time.Now().UnixNano() % 1000000) // Unique negative ID

	fileNode := &newIssueFileNode{
		cache:   r.cache,
		repo:    r.repo,
		title:   title,
		onDirty: r.onDirty,
	}

	// Create the inode with a unique ID
	stable := fs.StableAttr{
		Mode: fuse.S_IFREG,
		Ino:  uint64(uint32(pendingID) + 0x80000000), // High bit to avoid collision
	}

	child := r.NewInode(ctx, fileNode, stable)

	// Generate initial content template
	template := fmt.Sprintf(`---
repo: %s
state: open
labels: []
---

# %s

## Body

`, r.repo, title)

	handle := &newIssueFileHandle{
		cache:   r.cache,
		repo:    r.repo,
		title:   title,
		buffer:  []byte(template),
		dirty:   true,
		onDirty: r.onDirty,
	}

	// Set file attributes
	out.Mode = 0644
	out.Size = uint64(len(template))
	now := time.Now()
	out.SetTimes(&now, &now, &now)

	return child, handle, fuse.FOPEN_DIRECT_IO, 0
}

// newIssueFileNode represents a new issue file that hasn't been created on GitHub yet.
type newIssueFileNode struct {
	fs.Inode
	cache   *cache.DB
	repo    string
	title   string
	onDirty func()
}

var _ = (fs.NodeGetattrer)((*newIssueFileNode)(nil))
var _ = (fs.NodeOpener)((*newIssueFileNode)(nil))
var _ = (fs.NodeReader)((*newIssueFileNode)(nil))
var _ = (fs.NodeWriter)((*newIssueFileNode)(nil))
var _ = (fs.NodeFlusher)((*newIssueFileNode)(nil))
var _ = (fs.NodeSetattrer)((*newIssueFileNode)(nil))

// Getattr returns file attributes for a new issue file.
func (f *newIssueFileNode) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	// New issue file - return minimal attributes
	out.Mode = 0644
	out.Size = 0
	now := time.Now()
	out.SetTimes(&now, &now, &now)
	return 0
}

// Setattr handles attribute changes for a new issue file.
func (f *newIssueFileNode) Setattr(ctx context.Context, fh fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	if handle, ok := fh.(*newIssueFileHandle); ok {
		if sz, ok := in.GetSize(); ok {
			handle.mu.Lock()
			if sz == 0 {
				handle.buffer = []byte{}
			} else if int(sz) < len(handle.buffer) {
				handle.buffer = handle.buffer[:sz]
			}
			handle.dirty = true
			out.Size = uint64(len(handle.buffer))
			handle.mu.Unlock()
		}
	}

	out.Mode = 0644
	now := time.Now()
	out.SetTimes(&now, &now, &now)
	return 0
}

// Open opens a new issue file.
func (f *newIssueFileNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	// Generate initial template
	template := fmt.Sprintf(`---
repo: %s
state: open
labels: []
---

# %s

## Body

`, f.repo, f.title)

	handle := &newIssueFileHandle{
		cache:   f.cache,
		repo:    f.repo,
		title:   f.title,
		buffer:  []byte(template),
		dirty:   false,
		onDirty: f.onDirty,
	}

	return handle, fuse.FOPEN_DIRECT_IO, 0
}

// Read reads from a new issue file.
func (f *newIssueFileNode) Read(ctx context.Context, fh fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	handle, ok := fh.(*newIssueFileHandle)
	if !ok {
		return nil, syscall.EBADF
	}

	handle.mu.Lock()
	defer handle.mu.Unlock()

	if off >= int64(len(handle.buffer)) {
		return fuse.ReadResultData(nil), 0
	}

	end := off + int64(len(dest))
	if end > int64(len(handle.buffer)) {
		end = int64(len(handle.buffer))
	}

	return fuse.ReadResultData(handle.buffer[off:end]), 0
}

// Write writes to a new issue file.
func (f *newIssueFileNode) Write(ctx context.Context, fh fs.FileHandle, data []byte, off int64) (uint32, syscall.Errno) {
	handle, ok := fh.(*newIssueFileHandle)
	if !ok {
		return 0, syscall.EBADF
	}

	handle.mu.Lock()
	defer handle.mu.Unlock()

	endPos := int(off) + len(data)
	if endPos > maxFileSize {
		return 0, syscall.EFBIG
	}

	if endPos > len(handle.buffer) {
		newBuf := make([]byte, endPos)
		copy(newBuf, handle.buffer)
		handle.buffer = newBuf
	}

	copy(handle.buffer[off:], data)
	handle.dirty = true

	return uint32(len(data)), 0
}

// Flush is called when a new issue file is closed.
func (f *newIssueFileNode) Flush(ctx context.Context, fh fs.FileHandle) syscall.Errno {
	handle, ok := fh.(*newIssueFileHandle)
	if !ok {
		return 0
	}

	handle.mu.Lock()
	defer handle.mu.Unlock()

	if !handle.dirty {
		return 0
	}

	// Parse the content
	content := string(handle.buffer)
	parsed, err := md.FromMarkdown(content)
	if err != nil {
		logger.Warn("failed to parse new issue markdown: %v", err)
		return syscall.EIO
	}

	// Extract labels from frontmatter (if any)
	// For now, we don't parse labels from frontmatter (would need to extend ParsedIssue)
	// Just use empty labels
	var labels []string

	// Get title and body from parsed content
	title := parsed.Title
	if title == "" {
		title = f.title // Fallback to filename-derived title
	}
	body := parsed.Body

	// Add to pending issues
	_, err = f.cache.AddPendingIssue(f.repo, title, body, labels)
	if err != nil {
		logger.Warn("failed to add pending issue: %v", err)
		return syscall.EIO
	}

	// Trigger sync
	if f.onDirty != nil {
		f.onDirty()
	}

	handle.dirty = false
	return 0
}

// newIssueFileHandle represents an open file handle for a new issue.
type newIssueFileHandle struct {
	cache   *cache.DB
	repo    string
	title   string
	buffer  []byte
	dirty   bool
	onDirty func()
	mu      sync.Mutex
}

var _ = (fs.FileHandle)((*newIssueFileHandle)(nil))

// issueFileNode represents a single issue file.
type issueFileNode struct {
	fs.Inode
	cache   *cache.DB
	repo    string
	number  int
	onDirty func()
}

var _ = (fs.NodeGetattrer)((*issueFileNode)(nil))
var _ = (fs.NodeOpener)((*issueFileNode)(nil))
var _ = (fs.NodeReader)((*issueFileNode)(nil))
var _ = (fs.NodeWriter)((*issueFileNode)(nil))
var _ = (fs.NodeFlusher)((*issueFileNode)(nil))
var _ = (fs.NodeSetattrer)((*issueFileNode)(nil))

// Getattr returns file attributes.
func (f *issueFileNode) Getattr(ctx context.Context, fh fs.FileHandle, out *fuse.AttrOut) syscall.Errno {
	issue, err := f.cache.GetIssue(f.repo, f.number)
	if err != nil || issue == nil {
		return syscall.EIO
	}

	// Get comments from the cache
	comments, err := f.cache.GetComments(f.repo, f.number)
	if err != nil {
		comments = []cache.Comment{} // Use empty slice on error
	}

	content := md.ToMarkdown(issue, comments)

	out.Mode = 0644
	out.Size = uint64(len(content))
	out.Ino = uint64(f.number)

	// Set times from issue timestamps
	mtime := parseIssueTime(issue.UpdatedAt)
	ctime := parseIssueTime(issue.CreatedAt)
	out.SetTimes(&mtime, &mtime, &ctime)

	return 0
}

// Setattr handles attribute changes (e.g., truncate).
func (f *issueFileNode) Setattr(ctx context.Context, fh fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	// Handle truncate if requested
	if sz, ok := in.GetSize(); ok {
		// If there's an open file handle, update its buffer and report the new size
		if handle, ok := fh.(*issueFileHandle); ok {
			handle.mu.Lock()
			if sz == 0 {
				handle.buffer = []byte{}
			} else if int(sz) < len(handle.buffer) {
				handle.buffer = handle.buffer[:sz]
			}
			handle.dirty = true
			// Use the buffer size we just set, not a cache lookup
			newSize := uint64(len(handle.buffer))
			handle.mu.Unlock()

			out.Mode = 0644
			out.Ino = uint64(f.number)
			out.Size = newSize

			now := time.Now()
			out.SetTimes(&now, &now, &now)
			return 0
		}

		// No file handle - use the requested size directly
		out.Mode = 0644
		out.Ino = uint64(f.number)
		out.Size = sz

		now := time.Now()
		out.SetTimes(&now, &now, &now)
		return 0
	}

	// No truncate requested - get current size from cache
	issue, err := f.cache.GetIssue(f.repo, f.number)
	if err != nil || issue == nil {
		return syscall.EIO
	}

	out.Mode = 0644
	out.Ino = uint64(f.number)

	// Set times from issue timestamps
	mtime := parseIssueTime(issue.UpdatedAt)
	ctime := parseIssueTime(issue.CreatedAt)
	out.SetTimes(&mtime, &mtime, &ctime)

	// Get comments from the cache
	comments, err := f.cache.GetComments(f.repo, f.number)
	if err != nil {
		comments = []cache.Comment{} // Use empty slice on error
	}
	content := md.ToMarkdown(issue, comments)
	out.Size = uint64(len(content))

	return 0
}

// Open opens the file and returns a file handle.
func (f *issueFileNode) Open(ctx context.Context, flags uint32) (fs.FileHandle, uint32, syscall.Errno) {
	// Get the issue from cache
	issue, err := f.cache.GetIssue(f.repo, f.number)
	if err != nil || issue == nil {
		return nil, 0, syscall.EIO
	}

	// Get comments from the cache
	comments, err := f.cache.GetComments(f.repo, f.number)
	if err != nil {
		comments = []cache.Comment{} // Use empty slice on error
	}

	// Generate the content
	content := md.ToMarkdown(issue, comments)

	handle := &issueFileHandle{
		cache:   f.cache,
		repo:    f.repo,
		number:  f.number,
		buffer:  []byte(content),
		dirty:   false,
		onDirty: f.onDirty,
	}

	return handle, fuse.FOPEN_DIRECT_IO, 0
}

// Read reads data from the file.
func (f *issueFileNode) Read(ctx context.Context, fh fs.FileHandle, dest []byte, off int64) (fuse.ReadResult, syscall.Errno) {
	handle, ok := fh.(*issueFileHandle)
	if !ok {
		// No handle, read directly from cache
		issue, err := f.cache.GetIssue(f.repo, f.number)
		if err != nil || issue == nil {
			return nil, syscall.EIO
		}
		// Get comments from the cache
		comments, err := f.cache.GetComments(f.repo, f.number)
		if err != nil {
			comments = []cache.Comment{} // Use empty slice on error
		}
		content := md.ToMarkdown(issue, comments)
		if off >= int64(len(content)) {
			return fuse.ReadResultData(nil), 0
		}
		end := off + int64(len(dest))
		if end > int64(len(content)) {
			end = int64(len(content))
		}
		return fuse.ReadResultData([]byte(content[off:end])), 0
	}

	handle.mu.Lock()
	defer handle.mu.Unlock()

	if off >= int64(len(handle.buffer)) {
		return fuse.ReadResultData(nil), 0
	}

	end := off + int64(len(dest))
	if end > int64(len(handle.buffer)) {
		end = int64(len(handle.buffer))
	}

	return fuse.ReadResultData(handle.buffer[off:end]), 0
}

// Write writes data to the file.
func (f *issueFileNode) Write(ctx context.Context, fh fs.FileHandle, data []byte, off int64) (uint32, syscall.Errno) {
	handle, ok := fh.(*issueFileHandle)
	if !ok {
		return 0, syscall.EBADF
	}

	handle.mu.Lock()
	defer handle.mu.Unlock()

	// Check if write would exceed maximum file size
	endPos := int(off) + len(data)
	if endPos > maxFileSize {
		return 0, syscall.EFBIG // File too large
	}

	// Extend buffer if necessary
	if endPos > len(handle.buffer) {
		newBuf := make([]byte, endPos)
		copy(newBuf, handle.buffer)
		handle.buffer = newBuf
	}

	// Copy data to buffer
	copy(handle.buffer[off:], data)
	handle.dirty = true

	return uint32(len(data)), 0
}

// Flush is called when a file is closed. It saves changes to the cache.
func (f *issueFileNode) Flush(ctx context.Context, fh fs.FileHandle) syscall.Errno {
	handle, ok := fh.(*issueFileHandle)
	if !ok {
		return 0
	}

	handle.mu.Lock()
	defer handle.mu.Unlock()

	if !handle.dirty {
		return 0
	}

	// Parse the content to extract changes
	content := string(handle.buffer)
	parsed, err := md.FromMarkdown(content)
	if err != nil {
		// Can't parse the content, don't update
		logger.Warn("failed to parse markdown for issue #%d: %v", f.number, err)
		return syscall.EIO
	}

	// Get the original issue
	original, err := f.cache.GetIssue(f.repo, f.number)
	if err != nil || original == nil {
		return syscall.EIO
	}

	// Detect changes
	changes := md.DetectChanges(original, parsed)

	// Track if we need to trigger sync
	needsSync := false

	// If title or body changed, mark dirty in cache
	if changes.TitleChanged || changes.BodyChanged {
		var titlePtr, bodyPtr *string
		if changes.TitleChanged {
			titlePtr = &changes.NewTitle
		}
		if changes.BodyChanged {
			bodyPtr = &changes.NewBody
		}
		err = f.cache.MarkDirty(f.repo, f.number, titlePtr, bodyPtr)
		if err != nil {
			return syscall.EIO
		}
		needsSync = true
	}

	// Handle comment changes
	originalComments, err := f.cache.GetComments(f.repo, f.number)
	if err != nil {
		originalComments = []cache.Comment{}
	}

	newComments, editedComments := md.DetectCommentChanges(originalComments, parsed.Comments)

	// Add new comments to pending
	for _, nc := range newComments {
		err := f.cache.AddPendingComment(f.repo, f.number, nc.Body)
		if err != nil {
			logger.Warn("failed to add pending comment for issue #%d: %v", f.number, err)
		} else {
			needsSync = true
		}
	}

	// Mark edited comments as dirty
	for _, ec := range editedComments {
		err := f.cache.MarkCommentDirty(f.repo, ec.ID, ec.NewBody)
		if err != nil {
			logger.Warn("failed to mark comment %d as dirty: %v", ec.ID, err)
		} else {
			needsSync = true
		}
	}

	// Trigger sync engine callback if changes were made
	if needsSync && handle.onDirty != nil {
		handle.onDirty()
	}

	handle.dirty = false
	return 0
}

// issueFileHandle represents an open file handle for an issue.
type issueFileHandle struct {
	cache   *cache.DB
	repo    string
	number  int
	buffer  []byte
	dirty   bool
	onDirty func()
	mu      sync.Mutex
}

var _ = (fs.FileHandle)((*issueFileHandle)(nil))
