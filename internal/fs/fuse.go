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
	"github.com/JohanCodinha/ghissues/internal/md"
	"github.com/hanwen/go-fuse/v2/fs"
	"github.com/hanwen/go-fuse/v2/fuse"
)

const (
	// maxTitleLength is the maximum length for sanitized titles in filenames.
	maxTitleLength = 50
)

// filenameRegex matches issue filenames in the format: title[number].md
var filenameRegex = regexp.MustCompile(`^(.+)\[(\d+)\]\.md$`)

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

	// Set times
	now := time.Now()
	out.SetTimes(&now, &now, &now)

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

	// Set times
	now := time.Now()
	out.SetTimes(&now, &now, &now)

	return 0
}

// Setattr handles attribute changes (e.g., truncate).
func (f *issueFileNode) Setattr(ctx context.Context, fh fs.FileHandle, in *fuse.SetAttrIn, out *fuse.AttrOut) syscall.Errno {
	// Handle truncate if requested
	if sz, ok := in.GetSize(); ok {
		// If there's an open file handle, update its buffer
		if handle, ok := fh.(*issueFileHandle); ok {
			handle.mu.Lock()
			if sz == 0 {
				handle.buffer = []byte{}
			} else if int(sz) < len(handle.buffer) {
				handle.buffer = handle.buffer[:sz]
			}
			handle.dirty = true
			handle.mu.Unlock()
		}
	}

	// Update attributes in out
	issue, err := f.cache.GetIssue(f.repo, f.number)
	if err != nil || issue == nil {
		return syscall.EIO
	}

	out.Mode = 0644
	out.Ino = uint64(f.number)

	now := time.Now()
	out.SetTimes(&now, &now, &now)

	// If truncating, report the new size
	if sz, ok := in.GetSize(); ok {
		out.Size = sz
	} else {
		// Get comments from the cache
		comments, err := f.cache.GetComments(f.repo, f.number)
		if err != nil {
			comments = []cache.Comment{} // Use empty slice on error
		}
		content := md.ToMarkdown(issue, comments)
		out.Size = uint64(len(content))
	}

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

	// Extend buffer if necessary
	endPos := int(off) + len(data)
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
		return syscall.EIO
	}

	// Get the original issue
	original, err := f.cache.GetIssue(f.repo, f.number)
	if err != nil || original == nil {
		return syscall.EIO
	}

	// Detect changes
	changes := md.DetectChanges(original, parsed)

	// If body changed, mark dirty in cache
	if changes.BodyChanged {
		err = f.cache.MarkDirty(f.repo, f.number, changes.NewBody)
		if err != nil {
			return syscall.EIO
		}
		// Trigger sync engine callback if set
		if handle.onDirty != nil {
			handle.onDirty()
		}
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
