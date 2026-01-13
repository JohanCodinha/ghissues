package fs

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/JohanCodinha/ghissues/internal/cache"
	"github.com/hanwen/go-fuse/v2/fuse"
)

func TestSanitizeTitle(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{
			name:     "simple title",
			input:    "Crash on startup",
			expected: "crash-on-startup",
		},
		{
			name:     "title with special characters",
			input:    "Bug: Can't login! (urgent)",
			expected: "bug-cant-login-urgent",
		},
		{
			name:     "title with multiple spaces",
			input:    "Multiple   spaces   here",
			expected: "multiple-spaces-here",
		},
		{
			name:     "title with numbers",
			input:    "Fix issue 123 in module",
			expected: "fix-issue-123-in-module",
		},
		{
			name:     "empty title",
			input:    "",
			expected: "issue",
		},
		{
			name:     "only special characters",
			input:    "!@#$%^&*()",
			expected: "issue",
		},
		{
			name:     "title longer than 50 chars",
			input:    "This is a very long title that exceeds the maximum length allowed for sanitized filenames",
			expected: "this-is-a-very-long-title-that-exceeds-the-maximum",
		},
		{
			name:     "title with leading/trailing spaces",
			input:    "  Leading and trailing  ",
			expected: "leading-and-trailing",
		},
		{
			name:     "title with unicode characters",
			input:    "Fix für Deutsch",
			expected: "fix-fr-deutsch",
		},
		{
			name:     "title with underscores",
			input:    "some_function_name broken",
			expected: "somefunctionname-broken",
		},
		{
			name:     "title ending in dash after truncation",
			input:    "this-is-a-title-that-ends-in-a-dash-after-truncate-x",
			expected: "this-is-a-title-that-ends-in-a-dash-after-truncate",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := sanitizeTitle(tc.input)
			if result != tc.expected {
				t.Errorf("sanitizeTitle(%q) = %q, expected %q", tc.input, result, tc.expected)
			}
		})
	}
}

func TestMakeFilename(t *testing.T) {
	tests := []struct {
		name     string
		title    string
		number   int
		expected string
	}{
		{
			name:     "simple issue",
			title:    "Crash on startup",
			number:   1234,
			expected: "crash-on-startup[1234].md",
		},
		{
			name:     "issue with special chars",
			title:    "Bug: Login fails!",
			number:   42,
			expected: "bug-login-fails[42].md",
		},
		{
			name:     "issue number 1",
			title:    "First issue",
			number:   1,
			expected: "first-issue[1].md",
		},
		{
			name:     "large issue number",
			title:    "Old issue",
			number:   999999,
			expected: "old-issue[999999].md",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := makeFilename(tc.title, tc.number)
			if result != tc.expected {
				t.Errorf("makeFilename(%q, %d) = %q, expected %q", tc.title, tc.number, result, tc.expected)
			}
		})
	}
}

func TestParseFilename(t *testing.T) {
	tests := []struct {
		name           string
		filename       string
		expectedNumber int
		expectedOK     bool
	}{
		{
			name:           "valid filename",
			filename:       "crash-on-startup[1234].md",
			expectedNumber: 1234,
			expectedOK:     true,
		},
		{
			name:           "simple filename",
			filename:       "bug[42].md",
			expectedNumber: 42,
			expectedOK:     true,
		},
		{
			name:           "filename with dashes",
			filename:       "fix-the-login-bug[999].md",
			expectedNumber: 999,
			expectedOK:     true,
		},
		{
			name:           "large number",
			filename:       "issue[999999].md",
			expectedNumber: 999999,
			expectedOK:     true,
		},
		{
			name:           "missing .md extension",
			filename:       "crash-on-startup[1234]",
			expectedNumber: 0,
			expectedOK:     false,
		},
		{
			name:           "missing brackets",
			filename:       "crash-on-startup-1234.md",
			expectedNumber: 0,
			expectedOK:     false,
		},
		{
			name:           "wrong extension",
			filename:       "crash-on-startup[1234].txt",
			expectedNumber: 0,
			expectedOK:     false,
		},
		{
			name:           "no number in brackets",
			filename:       "crash-on-startup[abc].md",
			expectedNumber: 0,
			expectedOK:     false,
		},
		{
			name:           "empty brackets",
			filename:       "crash-on-startup[].md",
			expectedNumber: 0,
			expectedOK:     false,
		},
		{
			name:           "empty filename",
			filename:       "",
			expectedNumber: 0,
			expectedOK:     false,
		},
		{
			name:           "just extension",
			filename:       ".md",
			expectedNumber: 0,
			expectedOK:     false,
		},
		{
			name:           "brackets in title",
			filename:       "fix-array[0]-bug[123].md",
			expectedNumber: 123,
			expectedOK:     true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			number, ok := parseFilename(tc.filename)
			if ok != tc.expectedOK {
				t.Errorf("parseFilename(%q) ok = %v, expected %v", tc.filename, ok, tc.expectedOK)
			}
			if number != tc.expectedNumber {
				t.Errorf("parseFilename(%q) number = %d, expected %d", tc.filename, number, tc.expectedNumber)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	// Test that makeFilename and parseFilename work together correctly
	tests := []struct {
		title  string
		number int
	}{
		{"Crash on startup", 1234},
		{"Bug: Login fails!", 42},
		{"Feature request", 1},
		{"Old issue", 999999},
	}

	for _, tc := range tests {
		t.Run(tc.title, func(t *testing.T) {
			filename := makeFilename(tc.title, tc.number)
			parsedNumber, ok := parseFilename(filename)
			if !ok {
				t.Errorf("parseFilename failed for generated filename %q", filename)
				return
			}
			if parsedNumber != tc.number {
				t.Errorf("Round trip failed: makeFilename(%q, %d) = %q, parseFilename returned %d",
					tc.title, tc.number, filename, parsedNumber)
			}
		})
	}
}

// setupTestCache creates a temporary cache database with test issues.
func setupTestCache(t *testing.T) (*cache.DB, string) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := cache.InitDB(dbPath)
	if err != nil {
		t.Fatalf("failed to create test cache: %v", err)
	}

	return db, dbPath
}

// populateTestIssues adds test issues to the cache.
func populateTestIssues(t *testing.T, db *cache.DB, repo string, issues []cache.Issue) {
	t.Helper()

	for _, issue := range issues {
		issue.Repo = repo
		if err := db.UpsertIssue(issue); err != nil {
			t.Fatalf("failed to upsert test issue: %v", err)
		}
	}
}

// TestRootNode_Readdir tests that Readdir returns correct filenames for cached issues.
func TestRootNode_Readdir(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	issues := []cache.Issue{
		{Number: 1, Title: "First Issue", Body: "Body 1", State: "open"},
		{Number: 42, Title: "Bug: Login fails!", Body: "Body 2", State: "open"},
		{Number: 999, Title: "Feature Request", Body: "Body 3", State: "closed"},
	}
	populateTestIssues(t, db, repo, issues)

	root := &rootNode{
		cache: db,
		repo:  repo,
	}

	ctx := context.Background()
	dirStream, errno := root.Readdir(ctx)
	if errno != 0 {
		t.Fatalf("Readdir returned error: %v", errno)
	}

	// Collect all entries
	var entries []fuse.DirEntry
	for dirStream.HasNext() {
		entry, errno := dirStream.Next()
		if errno != 0 {
			t.Fatalf("DirStream.Next returned error: %v", errno)
		}
		entries = append(entries, entry)
	}

	// Verify count
	if len(entries) != len(issues) {
		t.Errorf("Readdir returned %d entries, expected %d", len(entries), len(issues))
	}

	// Verify filenames
	expectedNames := map[string]bool{
		"first-issue[1].md":       false,
		"bug-login-fails[42].md":  false,
		"feature-request[999].md": false,
	}

	for _, entry := range entries {
		if _, exists := expectedNames[entry.Name]; !exists {
			t.Errorf("unexpected filename: %s", entry.Name)
		} else {
			expectedNames[entry.Name] = true
		}
		// Verify mode is regular file
		if entry.Mode != fuse.S_IFREG {
			t.Errorf("entry %s has mode %v, expected S_IFREG", entry.Name, entry.Mode)
		}
	}

	// Check all expected names were found
	for name, found := range expectedNames {
		if !found {
			t.Errorf("expected filename not found: %s", name)
		}
	}
}

// TestRootNode_Readdir_EmptyCache tests Readdir with no issues.
func TestRootNode_Readdir_EmptyCache(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	root := &rootNode{
		cache: db,
		repo:  "test/repo",
	}

	ctx := context.Background()
	dirStream, errno := root.Readdir(ctx)
	if errno != 0 {
		t.Fatalf("Readdir returned error: %v", errno)
	}

	// Should have no entries
	if dirStream.HasNext() {
		t.Error("expected empty directory, but HasNext returned true")
	}
}

// TestRootNode_Lookup_InvalidFilename tests lookup error paths that don't need FUSE bridge.
// Note: Full Lookup testing requires FUSE mounting which needs root privileges.
// We test the filename parsing and cache lookup logic here.
func TestRootNode_Lookup_InvalidFilename(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	issues := []cache.Issue{
		{Number: 123, Title: "Test Issue", Body: "Test body content", State: "open", Author: "testuser"},
	}
	populateTestIssues(t, db, repo, issues)

	root := &rootNode{
		cache: db,
		repo:  repo,
	}

	ctx := context.Background()

	t.Run("invalid filename - wrong format", func(t *testing.T) {
		out := &fuse.EntryOut{}
		_, errno := root.Lookup(ctx, "invalid.txt", out)
		if errno != syscall.ENOENT {
			t.Errorf("expected ENOENT, got %v", errno)
		}
	})

	t.Run("invalid filename - nonexistent issue", func(t *testing.T) {
		out := &fuse.EntryOut{}
		_, errno := root.Lookup(ctx, "nonexistent[9999].md", out)
		if errno != syscall.ENOENT {
			t.Errorf("expected ENOENT, got %v", errno)
		}
	})

	t.Run("invalid filename - missing extension", func(t *testing.T) {
		out := &fuse.EntryOut{}
		_, errno := root.Lookup(ctx, "test-issue[123]", out)
		if errno != syscall.ENOENT {
			t.Errorf("expected ENOENT, got %v", errno)
		}
	})

	t.Run("invalid filename - no brackets", func(t *testing.T) {
		out := &fuse.EntryOut{}
		_, errno := root.Lookup(ctx, "test-issue.md", out)
		if errno != syscall.ENOENT {
			t.Errorf("expected ENOENT, got %v", errno)
		}
	})
}

// TestLookupParseAndCache tests the lookup logic without FUSE bridge.
// This validates that filename parsing and cache lookup work correctly.
func TestLookupParseAndCache(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	issues := []cache.Issue{
		{Number: 123, Title: "Test Issue", Body: "Test body content", State: "open", Author: "testuser"},
	}
	populateTestIssues(t, db, repo, issues)

	// Test that parsing the filename works
	number, ok := parseFilename("test-issue[123].md")
	if !ok {
		t.Fatal("parseFilename failed for valid filename")
	}
	if number != 123 {
		t.Errorf("parseFilename returned number %d, expected 123", number)
	}

	// Test that we can retrieve the issue from cache
	issue, err := db.GetIssue(repo, number)
	if err != nil {
		t.Fatalf("GetIssue returned error: %v", err)
	}
	if issue == nil {
		t.Fatal("GetIssue returned nil")
	}
	if issue.Title != "Test Issue" {
		t.Errorf("issue title = %q, expected %q", issue.Title, "Test Issue")
	}
}

// TestIssueFileNode_Getattr tests getting file attributes.
func TestIssueFileNode_Getattr(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	issues := []cache.Issue{
		{Number: 42, Title: "Test Issue", Body: "This is the body content.", State: "open", Author: "testuser"},
	}
	populateTestIssues(t, db, repo, issues)

	fileNode := &issueFileNode{
		cache:  db,
		repo:   repo,
		number: 42,
	}

	ctx := context.Background()
	out := &fuse.AttrOut{}
	errno := fileNode.Getattr(ctx, nil, out)
	if errno != 0 {
		t.Fatalf("Getattr returned error: %v", errno)
	}

	// Verify mode
	if out.Mode != 0644 {
		t.Errorf("mode = %o, expected 0644", out.Mode)
	}

	// Verify inode
	if out.Ino != 42 {
		t.Errorf("ino = %d, expected 42", out.Ino)
	}

	// Verify size matches expected markdown content length
	// The markdown includes frontmatter, title, and body
	if out.Size == 0 {
		t.Error("size should be > 0")
	}
}

// TestIssueFileNode_Getattr_NonexistentIssue tests Getattr for a missing issue.
func TestIssueFileNode_Getattr_NonexistentIssue(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	fileNode := &issueFileNode{
		cache:  db,
		repo:   "test/repo",
		number: 9999, // Does not exist
	}

	ctx := context.Background()
	out := &fuse.AttrOut{}
	errno := fileNode.Getattr(ctx, nil, out)
	if errno != syscall.EIO {
		t.Errorf("expected EIO, got %v", errno)
	}
}

// TestIssueFileNode_OpenReadWrite tests the full read/write cycle.
func TestIssueFileNode_OpenReadWrite(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	issues := []cache.Issue{
		{Number: 1, Title: "Test Issue", Body: "Original body content", State: "open", Author: "testuser"},
	}
	populateTestIssues(t, db, repo, issues)

	fileNode := &issueFileNode{
		cache:  db,
		repo:   repo,
		number: 1,
	}

	ctx := context.Background()

	// Open the file
	fh, flags, errno := fileNode.Open(ctx, 0)
	if errno != 0 {
		t.Fatalf("Open returned error: %v", errno)
	}
	if fh == nil {
		t.Fatal("Open returned nil file handle")
	}

	// Should use DIRECT_IO
	if flags&fuse.FOPEN_DIRECT_IO == 0 {
		t.Error("expected FOPEN_DIRECT_IO flag")
	}

	handle := fh.(*issueFileHandle)

	// Read the content
	dest := make([]byte, 4096)
	result, errno := fileNode.Read(ctx, fh, dest, 0)
	if errno != 0 {
		t.Fatalf("Read returned error: %v", errno)
	}

	resultData, _ := result.Bytes(nil)
	content := string(resultData)

	// Verify content contains expected parts
	if !strings.Contains(content, "# Test Issue") {
		t.Error("content should contain title")
	}
	if !strings.Contains(content, "Original body content") {
		t.Error("content should contain original body")
	}
	if !strings.Contains(content, "id: 1") {
		t.Error("content should contain frontmatter with id")
	}

	// Write new content
	newContent := strings.Replace(content, "Original body content", "Modified body content", 1)
	written, errno := fileNode.Write(ctx, fh, []byte(newContent), 0)
	if errno != 0 {
		t.Fatalf("Write returned error: %v", errno)
	}
	if int(written) != len(newContent) {
		t.Errorf("Write returned %d, expected %d", written, len(newContent))
	}

	// Verify buffer was updated
	if !strings.Contains(string(handle.buffer), "Modified body content") {
		t.Error("buffer should contain modified content")
	}

	// Verify dirty flag is set
	if !handle.dirty {
		t.Error("dirty flag should be set after write")
	}
}

// TestIssueFileNode_Write_ExtendBuffer tests writing beyond current buffer size.
func TestIssueFileNode_Write_ExtendBuffer(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	issues := []cache.Issue{
		{Number: 1, Title: "Test", Body: "Short", State: "open"},
	}
	populateTestIssues(t, db, repo, issues)

	fileNode := &issueFileNode{
		cache:  db,
		repo:   repo,
		number: 1,
	}

	ctx := context.Background()

	// Open file
	fh, _, errno := fileNode.Open(ctx, 0)
	if errno != 0 {
		t.Fatalf("Open returned error: %v", errno)
	}

	handle := fh.(*issueFileHandle)
	originalLen := len(handle.buffer)

	// Write at an offset beyond current buffer
	data := []byte("APPENDED DATA")
	offset := int64(originalLen + 100)
	written, errno := fileNode.Write(ctx, fh, data, offset)
	if errno != 0 {
		t.Fatalf("Write returned error: %v", errno)
	}
	if int(written) != len(data) {
		t.Errorf("Write returned %d, expected %d", written, len(data))
	}

	// Buffer should have grown
	expectedLen := int(offset) + len(data)
	if len(handle.buffer) != expectedLen {
		t.Errorf("buffer length = %d, expected %d", len(handle.buffer), expectedLen)
	}
}

// TestIssueFileNode_Flush_MarksDirty tests that Flush marks the cache dirty when body changes.
func TestIssueFileNode_Flush_MarksDirty(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	issues := []cache.Issue{
		{Number: 1, Title: "Test Issue", Body: "Original body", State: "open", Author: "testuser"},
	}
	populateTestIssues(t, db, repo, issues)

	var onDirtyCalled bool
	onDirty := func() { onDirtyCalled = true }

	fileNode := &issueFileNode{
		cache:   db,
		repo:    repo,
		number:  1,
		onDirty: onDirty,
	}

	ctx := context.Background()

	// Open and modify
	fh, _, errno := fileNode.Open(ctx, 0)
	if errno != 0 {
		t.Fatalf("Open returned error: %v", errno)
	}

	handle := fh.(*issueFileHandle)

	// Modify the body in the buffer
	content := string(handle.buffer)
	newContent := strings.Replace(content, "Original body", "Changed body", 1)
	handle.buffer = []byte(newContent)
	handle.dirty = true

	// Flush
	errno = fileNode.Flush(ctx, fh)
	if errno != 0 {
		t.Fatalf("Flush returned error: %v", errno)
	}

	// Verify onDirty callback was called
	if !onDirtyCalled {
		t.Error("onDirty callback should have been called")
	}

	// Verify issue is marked dirty in cache
	issue, err := db.GetIssue(repo, 1)
	if err != nil {
		t.Fatalf("failed to get issue: %v", err)
	}
	if !issue.Dirty {
		t.Error("issue should be marked dirty in cache")
	}
	if issue.Body != "Changed body" {
		t.Errorf("issue body = %q, expected %q", issue.Body, "Changed body")
	}
}

// TestIssueFileNode_Flush_NoChanges tests that Flush does nothing when content unchanged.
func TestIssueFileNode_Flush_NoChanges(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	issues := []cache.Issue{
		{Number: 1, Title: "Test Issue", Body: "Original body", State: "open"},
	}
	populateTestIssues(t, db, repo, issues)

	var onDirtyCalled bool
	onDirty := func() { onDirtyCalled = true }

	fileNode := &issueFileNode{
		cache:   db,
		repo:    repo,
		number:  1,
		onDirty: onDirty,
	}

	ctx := context.Background()

	// Open without modification
	fh, _, errno := fileNode.Open(ctx, 0)
	if errno != 0 {
		t.Fatalf("Open returned error: %v", errno)
	}

	// Flush without setting dirty flag
	errno = fileNode.Flush(ctx, fh)
	if errno != 0 {
		t.Fatalf("Flush returned error: %v", errno)
	}

	// Verify onDirty callback was NOT called
	if onDirtyCalled {
		t.Error("onDirty callback should not have been called")
	}
}

// TestIssueFileNode_Setattr_Truncate tests the truncate operation (vim behavior).
func TestIssueFileNode_Setattr_Truncate(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	issues := []cache.Issue{
		{Number: 1, Title: "Test Issue", Body: "Original body content here", State: "open"},
	}
	populateTestIssues(t, db, repo, issues)

	fileNode := &issueFileNode{
		cache:  db,
		repo:   repo,
		number: 1,
	}

	ctx := context.Background()

	// Open file to get a handle
	fh, _, errno := fileNode.Open(ctx, 0)
	if errno != 0 {
		t.Fatalf("Open returned error: %v", errno)
	}

	handle := fh.(*issueFileHandle)
	originalLen := len(handle.buffer)

	t.Run("truncate to zero", func(t *testing.T) {
		in := &fuse.SetAttrIn{}
		in.Valid = fuse.FATTR_SIZE
		in.Size = 0

		out := &fuse.AttrOut{}
		errno := fileNode.Setattr(ctx, fh, in, out)
		if errno != 0 {
			t.Fatalf("Setattr returned error: %v", errno)
		}

		// Verify buffer is now empty
		if len(handle.buffer) != 0 {
			t.Errorf("buffer length = %d, expected 0", len(handle.buffer))
		}

		// Verify dirty flag is set
		if !handle.dirty {
			t.Error("dirty flag should be set after truncate")
		}

		// Verify reported size is 0
		if out.Size != 0 {
			t.Errorf("reported size = %d, expected 0", out.Size)
		}
	})

	// Reset for next test
	handle.buffer = make([]byte, originalLen)
	copy(handle.buffer, "Some test content here")
	handle.dirty = false

	t.Run("truncate to smaller size", func(t *testing.T) {
		in := &fuse.SetAttrIn{}
		in.Valid = fuse.FATTR_SIZE
		in.Size = 10

		out := &fuse.AttrOut{}
		errno := fileNode.Setattr(ctx, fh, in, out)
		if errno != 0 {
			t.Fatalf("Setattr returned error: %v", errno)
		}

		// Verify buffer is truncated
		if len(handle.buffer) != 10 {
			t.Errorf("buffer length = %d, expected 10", len(handle.buffer))
		}

		// Verify dirty flag is set
		if !handle.dirty {
			t.Error("dirty flag should be set after truncate")
		}

		// Verify reported size
		if out.Size != 10 {
			t.Errorf("reported size = %d, expected 10", out.Size)
		}
	})
}

// TestIssueFileNode_Setattr_TruncateLarger tests truncating larger than current size (should be no-op for buffer).
func TestIssueFileNode_Setattr_TruncateLarger(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	issues := []cache.Issue{
		{Number: 1, Title: "Test", Body: "Short", State: "open"},
	}
	populateTestIssues(t, db, repo, issues)

	fileNode := &issueFileNode{
		cache:  db,
		repo:   repo,
		number: 1,
	}

	ctx := context.Background()

	// Open file
	fh, _, errno := fileNode.Open(ctx, 0)
	if errno != 0 {
		t.Fatalf("Open returned error: %v", errno)
	}

	handle := fh.(*issueFileHandle)
	originalLen := len(handle.buffer)

	// Try to truncate to larger size
	in := &fuse.SetAttrIn{}
	in.Valid = fuse.FATTR_SIZE
	in.Size = uint64(originalLen + 100)

	out := &fuse.AttrOut{}
	errno = fileNode.Setattr(ctx, fh, in, out)
	if errno != 0 {
		t.Fatalf("Setattr returned error: %v", errno)
	}

	// Buffer should NOT have grown (truncate larger is a no-op)
	if len(handle.buffer) != originalLen {
		t.Errorf("buffer length = %d, expected %d (unchanged)", len(handle.buffer), originalLen)
	}
}

// TestIssueFileNode_Setattr_NoHandle tests Setattr without a file handle.
func TestIssueFileNode_Setattr_NoHandle(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	issues := []cache.Issue{
		{Number: 1, Title: "Test Issue", Body: "Body content", State: "open"},
	}
	populateTestIssues(t, db, repo, issues)

	fileNode := &issueFileNode{
		cache:  db,
		repo:   repo,
		number: 1,
	}

	ctx := context.Background()

	// Call Setattr with nil handle
	in := &fuse.SetAttrIn{}
	in.Valid = fuse.FATTR_SIZE
	in.Size = 0

	out := &fuse.AttrOut{}
	errno := fileNode.Setattr(ctx, nil, in, out)
	if errno != 0 {
		t.Fatalf("Setattr returned error: %v", errno)
	}

	// Should return the requested size
	if out.Size != 0 {
		t.Errorf("reported size = %d, expected 0", out.Size)
	}
}

// TestIssueFileNode_Read_OffsetBeyondEnd tests reading at an offset beyond file end.
func TestIssueFileNode_Read_OffsetBeyondEnd(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	issues := []cache.Issue{
		{Number: 1, Title: "Test", Body: "Short body", State: "open"},
	}
	populateTestIssues(t, db, repo, issues)

	fileNode := &issueFileNode{
		cache:  db,
		repo:   repo,
		number: 1,
	}

	ctx := context.Background()

	// Open file
	fh, _, errno := fileNode.Open(ctx, 0)
	if errno != 0 {
		t.Fatalf("Open returned error: %v", errno)
	}

	handle := fh.(*issueFileHandle)

	// Read at offset beyond buffer length
	dest := make([]byte, 100)
	result, errno := fileNode.Read(ctx, fh, dest, int64(len(handle.buffer)+100))
	if errno != 0 {
		t.Fatalf("Read returned error: %v", errno)
	}

	// Should return empty result
	data, _ := result.Bytes(nil)
	if len(data) != 0 {
		t.Errorf("expected empty result, got %d bytes", len(data))
	}
}

// TestIssueFileNode_Read_PartialRead tests reading part of the file.
func TestIssueFileNode_Read_PartialRead(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	issues := []cache.Issue{
		{Number: 1, Title: "Test", Body: "Body content", State: "open"},
	}
	populateTestIssues(t, db, repo, issues)

	fileNode := &issueFileNode{
		cache:  db,
		repo:   repo,
		number: 1,
	}

	ctx := context.Background()

	// Open file
	fh, _, errno := fileNode.Open(ctx, 0)
	if errno != 0 {
		t.Fatalf("Open returned error: %v", errno)
	}

	handle := fh.(*issueFileHandle)
	bufferLen := len(handle.buffer)

	// Read 10 bytes starting at offset 5
	dest := make([]byte, 10)
	result, errno := fileNode.Read(ctx, fh, dest, 5)
	if errno != 0 {
		t.Fatalf("Read returned error: %v", errno)
	}

	data, _ := result.Bytes(nil)

	// Should have read the correct slice
	expected := handle.buffer[5:15]
	if string(data) != string(expected) {
		t.Errorf("read data = %q, expected %q", string(data), string(expected))
	}

	// Read beyond end (should truncate)
	dest = make([]byte, 100)
	result, errno = fileNode.Read(ctx, fh, dest, int64(bufferLen-5))
	if errno != 0 {
		t.Fatalf("Read returned error: %v", errno)
	}

	data, _ = result.Bytes(nil)
	if len(data) != 5 {
		t.Errorf("expected 5 bytes, got %d", len(data))
	}
}

// TestIssueFileNode_Read_NoHandle tests reading without a proper file handle.
func TestIssueFileNode_Read_NoHandle(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	issues := []cache.Issue{
		{Number: 1, Title: "Test Issue", Body: "Test body content", State: "open"},
	}
	populateTestIssues(t, db, repo, issues)

	fileNode := &issueFileNode{
		cache:  db,
		repo:   repo,
		number: 1,
	}

	ctx := context.Background()

	// Read without a handle (nil)
	dest := make([]byte, 4096)
	result, errno := fileNode.Read(ctx, nil, dest, 0)
	if errno != 0 {
		t.Fatalf("Read returned error: %v", errno)
	}

	resultData, _ := result.Bytes(nil)
	content := string(resultData)

	// Should still read from cache
	if !strings.Contains(content, "Test body content") {
		t.Error("content should contain body from cache")
	}
}

// TestIssueFileNode_Write_NoHandle tests writing without a proper file handle.
func TestIssueFileNode_Write_NoHandle(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	fileNode := &issueFileNode{
		cache:  db,
		repo:   "test/repo",
		number: 1,
	}

	ctx := context.Background()

	// Write without a handle (nil)
	_, errno := fileNode.Write(ctx, nil, []byte("test"), 0)
	if errno != syscall.EBADF {
		t.Errorf("expected EBADF, got %v", errno)
	}
}

// TestIssueFileNode_WithComments tests that comments are included in the output.
func TestIssueFileNode_WithComments(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	issues := []cache.Issue{
		{Number: 1, Title: "Issue with Comments", Body: "Main body", State: "open"},
	}
	populateTestIssues(t, db, repo, issues)

	// Add comments
	comments := []cache.Comment{
		{ID: 100, Author: "user1", Body: "First comment", CreatedAt: "2025-01-01T10:00:00Z"},
		{ID: 101, Author: "user2", Body: "Second comment", CreatedAt: "2025-01-01T11:00:00Z"},
	}
	if err := db.UpsertComments(repo, 1, comments); err != nil {
		t.Fatalf("failed to add comments: %v", err)
	}

	fileNode := &issueFileNode{
		cache:  db,
		repo:   repo,
		number: 1,
	}

	ctx := context.Background()

	// Open and read
	fh, _, errno := fileNode.Open(ctx, 0)
	if errno != 0 {
		t.Fatalf("Open returned error: %v", errno)
	}

	dest := make([]byte, 8192)
	result, errno := fileNode.Read(ctx, fh, dest, 0)
	if errno != 0 {
		t.Fatalf("Read returned error: %v", errno)
	}

	resultData, _ := result.Bytes(nil)
	content := string(resultData)

	// Verify comments section
	if !strings.Contains(content, "## Comments") {
		t.Error("content should contain Comments section")
	}
	if !strings.Contains(content, "First comment") {
		t.Error("content should contain first comment")
	}
	if !strings.Contains(content, "Second comment") {
		t.Error("content should contain second comment")
	}
	if !strings.Contains(content, "user1") {
		t.Error("content should contain first comment author")
	}
}

// TestConcurrentAccess tests concurrent read/write operations on the same file.
// Run with -race flag to detect data races.
func TestConcurrentAccess(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	issues := []cache.Issue{
		{Number: 1, Title: "Concurrent Test Issue", Body: "Initial body content", State: "open"},
	}
	populateTestIssues(t, db, repo, issues)

	fileNode := &issueFileNode{
		cache:  db,
		repo:   repo,
		number: 1,
	}

	ctx := context.Background()

	// Open a single file handle
	fh, _, errno := fileNode.Open(ctx, 0)
	if errno != 0 {
		t.Fatalf("Open returned error: %v", errno)
	}

	const numGoroutines = 10
	const opsPerGoroutine = 100

	var wg sync.WaitGroup
	var readCount, writeCount int64

	// Run concurrent reads and writes
	for i := 0; i < numGoroutines; i++ {
		wg.Add(2)

		// Reader goroutine
		go func() {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				dest := make([]byte, 1024)
				_, err := fileNode.Read(ctx, fh, dest, 0)
				if err != 0 {
					t.Errorf("concurrent read error: %v", err)
				}
				atomic.AddInt64(&readCount, 1)
			}
		}()

		// Writer goroutine
		go func(id int) {
			defer wg.Done()
			for j := 0; j < opsPerGoroutine; j++ {
				data := []byte("data from goroutine")
				_, err := fileNode.Write(ctx, fh, data, int64(id*100+j))
				if err != 0 {
					t.Errorf("concurrent write error: %v", err)
				}
				atomic.AddInt64(&writeCount, 1)
			}
		}(i)
	}

	wg.Wait()

	expectedOps := int64(numGoroutines * opsPerGoroutine)
	if readCount != expectedOps {
		t.Errorf("read count = %d, expected %d", readCount, expectedOps)
	}
	if writeCount != expectedOps {
		t.Errorf("write count = %d, expected %d", writeCount, expectedOps)
	}
}

// TestConcurrentTruncateAndWrite tests concurrent truncate and write operations.
// This specifically tests the race condition fix in Setattr.
func TestConcurrentTruncateAndWrite(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	issues := []cache.Issue{
		{Number: 1, Title: "Race Test", Body: strings.Repeat("x", 1000), State: "open"},
	}
	populateTestIssues(t, db, repo, issues)

	fileNode := &issueFileNode{
		cache:  db,
		repo:   repo,
		number: 1,
	}

	ctx := context.Background()

	// Open file
	fh, _, errno := fileNode.Open(ctx, 0)
	if errno != 0 {
		t.Fatalf("Open returned error: %v", errno)
	}

	const iterations = 100

	var wg sync.WaitGroup

	// Concurrent truncate operations (simulating vim behavior)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			in := &fuse.SetAttrIn{}
			in.Valid = fuse.FATTR_SIZE
			in.Size = 0

			out := &fuse.AttrOut{}
			fileNode.Setattr(ctx, fh, in, out)
		}
	}()

	// Concurrent write operations
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			data := []byte("new content after truncate")
			fileNode.Write(ctx, fh, data, 0)
		}
	}()

	// Concurrent read operations
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < iterations; i++ {
			dest := make([]byte, 1024)
			fileNode.Read(ctx, fh, dest, 0)
		}
	}()

	wg.Wait()

	// If we get here without panicking, the race condition is properly handled
	t.Log("concurrent truncate/write/read completed without race")
}

// TestConcurrentMultipleFileHandles tests concurrent access with multiple file handles.
func TestConcurrentMultipleFileHandles(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	issues := []cache.Issue{
		{Number: 1, Title: "Multi-handle Test", Body: "Test body", State: "open"},
	}
	populateTestIssues(t, db, repo, issues)

	fileNode := &issueFileNode{
		cache:  db,
		repo:   repo,
		number: 1,
	}

	ctx := context.Background()

	const numHandles = 5
	const opsPerHandle = 50

	var wg sync.WaitGroup

	for i := 0; i < numHandles; i++ {
		// Each goroutine opens its own file handle
		wg.Add(1)
		go func(id int) {
			defer wg.Done()

			// Open file handle
			fh, _, errno := fileNode.Open(ctx, 0)
			if errno != 0 {
				t.Errorf("goroutine %d: Open error: %v", id, errno)
				return
			}

			// Perform operations
			for j := 0; j < opsPerHandle; j++ {
				// Read
				dest := make([]byte, 512)
				_, errno := fileNode.Read(ctx, fh, dest, 0)
				if errno != 0 {
					t.Errorf("goroutine %d: Read error: %v", id, errno)
				}

				// Write
				data := []byte("data")
				_, errno = fileNode.Write(ctx, fh, data, int64(j*10))
				if errno != 0 {
					t.Errorf("goroutine %d: Write error: %v", id, errno)
				}
			}
		}(i)
	}

	wg.Wait()
}

// TestNewFS tests the NewFS constructor.
func TestNewFS(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	onDirtyCalled := false
	onDirty := func() { onDirtyCalled = true }

	fsys := NewFS(db, "owner/repo", "/mnt/issues", onDirty)

	if fsys == nil {
		t.Fatal("NewFS returned nil")
	}
	if fsys.cache != db {
		t.Error("cache not set correctly")
	}
	if fsys.repo != "owner/repo" {
		t.Errorf("repo = %q, expected %q", fsys.repo, "owner/repo")
	}
	if fsys.mountpoint != "/mnt/issues" {
		t.Errorf("mountpoint = %q, expected %q", fsys.mountpoint, "/mnt/issues")
	}

	// Test that onDirty is set correctly
	fsys.onDirty()
	if !onDirtyCalled {
		t.Error("onDirty callback not set correctly")
	}
}

// TestUnmountNilServer tests Unmount when server is nil.
func TestUnmountNilServer(t *testing.T) {
	fsys := &FS{
		server: nil,
	}

	err := fsys.Unmount()
	if err != nil {
		t.Errorf("Unmount with nil server returned error: %v", err)
	}
}

// TestFilenameRoundTrip_DifferentTitleFormats tests filename generation and parsing with various title formats.
// This tests the lookup logic without requiring FUSE bridge.
func TestFilenameRoundTrip_DifferentTitleFormats(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	issues := []cache.Issue{
		{Number: 1, Title: "Simple", Body: "Body", State: "open"},
		{Number: 2, Title: "With Spaces Here", Body: "Body", State: "open"},
		{Number: 3, Title: "Special!@#Characters", Body: "Body", State: "open"},
		{Number: 4, Title: "", Body: "Body", State: "open"}, // Empty title
	}
	populateTestIssues(t, db, repo, issues)

	tests := []struct {
		name     string
		filename string
		wantNum  int
		wantOK   bool
	}{
		{"simple title", "simple[1].md", 1, true},
		{"spaces in title", "with-spaces-here[2].md", 2, true},
		{"special chars", "specialcharacters[3].md", 3, true},
		{"empty title", "issue[4].md", 4, true},
		{"wrong number", "simple[999].md", 999, true}, // Parse succeeds, but issue won't exist in cache
		{"invalid format", "simple.md", 0, false},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			number, ok := parseFilename(tc.filename)
			if ok != tc.wantOK {
				t.Errorf("parseFilename(%q) ok = %v, want %v", tc.filename, ok, tc.wantOK)
			}
			if ok && number != tc.wantNum {
				t.Errorf("parseFilename(%q) number = %d, want %d", tc.filename, number, tc.wantNum)
			}

			// Verify cache lookup for existing issues
			if ok && tc.wantNum <= 4 {
				issue, err := db.GetIssue(repo, number)
				if err != nil {
					t.Fatalf("GetIssue returned error: %v", err)
				}
				if issue == nil {
					t.Fatalf("GetIssue returned nil for issue %d", number)
				}
			}

			// Verify cache lookup fails for non-existent issues
			if ok && tc.wantNum == 999 {
				issue, err := db.GetIssue(repo, number)
				if err != nil {
					t.Fatalf("GetIssue returned error: %v", err)
				}
				if issue != nil {
					t.Error("expected nil issue for non-existent number")
				}
			}
		})
	}
}

// TestIssueFileHandle_BufferIsolation tests that each file handle has isolated buffer.
func TestIssueFileHandle_BufferIsolation(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	issues := []cache.Issue{
		{Number: 1, Title: "Test", Body: "Original body", State: "open"},
	}
	populateTestIssues(t, db, repo, issues)

	fileNode := &issueFileNode{
		cache:  db,
		repo:   repo,
		number: 1,
	}

	ctx := context.Background()

	// Open two handles
	fh1, _, _ := fileNode.Open(ctx, 0)
	fh2, _, _ := fileNode.Open(ctx, 0)

	handle1 := fh1.(*issueFileHandle)
	handle2 := fh2.(*issueFileHandle)

	// Modify handle1's buffer
	handle1.buffer = append(handle1.buffer, []byte(" - modified by handle1")...)

	// handle2's buffer should be unchanged
	if strings.Contains(string(handle2.buffer), "modified by handle1") {
		t.Error("handle2's buffer should be independent of handle1's modifications")
	}

	// Verify they started with the same content
	if !strings.Contains(string(handle2.buffer), "Original body") {
		t.Error("handle2 should have original content")
	}
}

// TestParseIssueTime tests the parseIssueTime helper function.
func TestParseIssueTime(t *testing.T) {
	tests := []struct {
		name      string
		timestamp string
		wantTime  time.Time
		checkNow  bool // If true, verify result is close to time.Now()
	}{
		{
			name:      "valid RFC3339 timestamp",
			timestamp: "2024-06-15T14:30:00Z",
			wantTime:  time.Date(2024, 6, 15, 14, 30, 0, 0, time.UTC),
			checkNow:  false,
		},
		{
			name:      "valid RFC3339 with timezone",
			timestamp: "2024-01-01T00:00:00+05:00",
			wantTime:  time.Date(2024, 1, 1, 0, 0, 0, 0, time.FixedZone("", 5*60*60)),
			checkNow:  false,
		},
		{
			name:      "empty timestamp",
			timestamp: "",
			checkNow:  true,
		},
		{
			name:      "invalid timestamp",
			timestamp: "not-a-timestamp",
			checkNow:  true,
		},
		{
			name:      "partial timestamp",
			timestamp: "2024-06-15",
			checkNow:  true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			before := time.Now()
			result := parseIssueTime(tc.timestamp)
			after := time.Now()

			if tc.checkNow {
				// Result should be between before and after (i.e., close to time.Now())
				if result.Before(before) || result.After(after) {
					t.Errorf("parseIssueTime(%q) = %v, expected time close to Now()", tc.timestamp, result)
				}
			} else {
				if !result.Equal(tc.wantTime) {
					t.Errorf("parseIssueTime(%q) = %v, want %v", tc.timestamp, result, tc.wantTime)
				}
			}
		})
	}
}

// TestGetattr_ReturnsCorrectMtime tests that mtime reflects issue UpdatedAt.
func TestGetattr_ReturnsCorrectMtime(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	// Use specific timestamps
	createdAt := "2024-01-15T10:00:00Z"
	updatedAt := "2024-06-20T15:30:00Z"

	issues := []cache.Issue{
		{
			Number:    42,
			Title:     "Test Issue",
			Body:      "Test body content.",
			State:     "open",
			Author:    "testuser",
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		},
	}
	populateTestIssues(t, db, repo, issues)

	fileNode := &issueFileNode{
		cache:  db,
		repo:   repo,
		number: 42,
	}

	ctx := context.Background()
	out := &fuse.AttrOut{}
	errno := fileNode.Getattr(ctx, nil, out)
	if errno != 0 {
		t.Fatalf("Getattr returned error: %v", errno)
	}

	// Parse expected times
	expectedMtime, _ := time.Parse(time.RFC3339, updatedAt)
	expectedCtime, _ := time.Parse(time.RFC3339, createdAt)

	// Verify mtime matches UpdatedAt
	actualMtime := time.Unix(int64(out.Mtime), int64(out.Mtimensec))
	if !actualMtime.Equal(expectedMtime) {
		t.Errorf("mtime = %v, expected %v", actualMtime, expectedMtime)
	}

	// Verify atime matches UpdatedAt (we use mtime for atime)
	actualAtime := time.Unix(int64(out.Atime), int64(out.Atimensec))
	if !actualAtime.Equal(expectedMtime) {
		t.Errorf("atime = %v, expected %v", actualAtime, expectedMtime)
	}

	// Verify ctime matches CreatedAt
	actualCtime := time.Unix(int64(out.Ctime), int64(out.Ctimensec))
	if !actualCtime.Equal(expectedCtime) {
		t.Errorf("ctime = %v, expected %v", actualCtime, expectedCtime)
	}
}

// TestGetattr_FallbackOnInvalidTimestamp tests graceful handling of empty/invalid timestamps.
func TestGetattr_FallbackOnInvalidTimestamp(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"

	tests := []struct {
		name      string
		updatedAt string
		createdAt string
	}{
		{
			name:      "empty timestamps",
			updatedAt: "",
			createdAt: "",
		},
		{
			name:      "invalid UpdatedAt",
			updatedAt: "not-a-valid-timestamp",
			createdAt: "2024-01-01T00:00:00Z",
		},
		{
			name:      "invalid CreatedAt",
			updatedAt: "2024-06-15T12:00:00Z",
			createdAt: "garbage",
		},
		{
			name:      "both invalid",
			updatedAt: "invalid1",
			createdAt: "invalid2",
		},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			issueNum := 100 + i
			issues := []cache.Issue{
				{
					Number:    issueNum,
					Title:     "Test Issue",
					Body:      "Body",
					State:     "open",
					UpdatedAt: tc.updatedAt,
					CreatedAt: tc.createdAt,
				},
			}
			populateTestIssues(t, db, repo, issues)

			fileNode := &issueFileNode{
				cache:  db,
				repo:   repo,
				number: issueNum,
			}

			ctx := context.Background()
			out := &fuse.AttrOut{}

			before := time.Now()
			errno := fileNode.Getattr(ctx, nil, out)
			after := time.Now()

			// Should not crash or return an error
			if errno != 0 {
				t.Fatalf("Getattr returned error: %v", errno)
			}

			// Times should be valid (either parsed or fallback to Now())
			actualMtime := time.Unix(int64(out.Mtime), int64(out.Mtimensec))

			// If UpdatedAt is invalid, mtime should be close to Now()
			if tc.updatedAt == "" || tc.updatedAt == "not-a-valid-timestamp" || tc.updatedAt == "invalid1" {
				if actualMtime.Before(before.Add(-time.Second)) || actualMtime.After(after.Add(time.Second)) {
					t.Errorf("mtime = %v, expected time close to Now() (between %v and %v)", actualMtime, before, after)
				}
			}

			// If CreatedAt is invalid, ctime should be close to Now()
			actualCtime := time.Unix(int64(out.Ctime), int64(out.Ctimensec))
			if tc.createdAt == "" || tc.createdAt == "garbage" || tc.createdAt == "invalid2" {
				if actualCtime.Before(before.Add(-time.Second)) || actualCtime.After(after.Add(time.Second)) {
					t.Errorf("ctime = %v, expected time close to Now() (between %v and %v)", actualCtime, before, after)
				}
			}

			// Verify other attributes are still set correctly
			if out.Mode != 0644 {
				t.Errorf("mode = %o, expected 0644", out.Mode)
			}
			if out.Ino != uint64(issueNum) {
				t.Errorf("ino = %d, expected %d", out.Ino, issueNum)
			}
		})
	}
}

// TestLookup_TimestampParsing tests that timestamp parsing logic used by Lookup is correct.
// Note: Full Lookup testing requires FUSE mounting which needs the bridge initialized.
// We test the timestamp parsing and Getattr behavior here since Lookup uses the same logic.
func TestLookup_TimestampParsing(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	createdAt := "2023-03-10T08:00:00Z"
	updatedAt := "2023-12-25T18:45:00Z"

	issues := []cache.Issue{
		{
			Number:    123,
			Title:     "Lookup Test Issue",
			Body:      "Body content",
			State:     "open",
			Author:    "testuser",
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		},
	}
	populateTestIssues(t, db, repo, issues)

	// Verify the issue can be retrieved and timestamps are preserved
	issue, err := db.GetIssue(repo, 123)
	if err != nil {
		t.Fatalf("GetIssue returned error: %v", err)
	}
	if issue == nil {
		t.Fatal("GetIssue returned nil")
	}

	// Verify parseIssueTime correctly parses the timestamps
	expectedMtime, _ := time.Parse(time.RFC3339, updatedAt)
	expectedCtime, _ := time.Parse(time.RFC3339, createdAt)

	actualMtime := parseIssueTime(issue.UpdatedAt)
	if !actualMtime.Equal(expectedMtime) {
		t.Errorf("parseIssueTime(UpdatedAt) = %v, expected %v", actualMtime, expectedMtime)
	}

	actualCtime := parseIssueTime(issue.CreatedAt)
	if !actualCtime.Equal(expectedCtime) {
		t.Errorf("parseIssueTime(CreatedAt) = %v, expected %v", actualCtime, expectedCtime)
	}

	// Also verify Getattr (which uses the same logic as Lookup for timestamps)
	fileNode := &issueFileNode{
		cache:  db,
		repo:   repo,
		number: 123,
	}

	ctx := context.Background()
	out := &fuse.AttrOut{}
	errno := fileNode.Getattr(ctx, nil, out)
	if errno != 0 {
		t.Fatalf("Getattr returned error: %v", errno)
	}

	// Verify mtime matches UpdatedAt
	getattrMtime := time.Unix(int64(out.Mtime), int64(out.Mtimensec))
	if !getattrMtime.Equal(expectedMtime) {
		t.Errorf("Getattr mtime = %v, expected %v", getattrMtime, expectedMtime)
	}

	// Verify ctime matches CreatedAt
	getattrCtime := time.Unix(int64(out.Ctime), int64(out.Ctimensec))
	if !getattrCtime.Equal(expectedCtime) {
		t.Errorf("Getattr ctime = %v, expected %v", getattrCtime, expectedCtime)
	}
}

// TestSetattr_NoTruncate_ReturnsCorrectMtime tests that Setattr without truncate uses issue timestamps.
func TestSetattr_NoTruncate_ReturnsCorrectMtime(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	createdAt := "2024-02-01T09:00:00Z"
	updatedAt := "2024-08-15T16:00:00Z"

	issues := []cache.Issue{
		{
			Number:    77,
			Title:     "Setattr Test Issue",
			Body:      "Body content",
			State:     "open",
			CreatedAt: createdAt,
			UpdatedAt: updatedAt,
		},
	}
	populateTestIssues(t, db, repo, issues)

	fileNode := &issueFileNode{
		cache:  db,
		repo:   repo,
		number: 77,
	}

	ctx := context.Background()

	// Call Setattr without size change (no truncate)
	in := &fuse.SetAttrIn{}
	// Don't set FATTR_SIZE - this simulates a non-truncate Setattr call
	out := &fuse.AttrOut{}

	errno := fileNode.Setattr(ctx, nil, in, out)
	if errno != 0 {
		t.Fatalf("Setattr returned error: %v", errno)
	}

	// Parse expected times
	expectedMtime, _ := time.Parse(time.RFC3339, updatedAt)
	expectedCtime, _ := time.Parse(time.RFC3339, createdAt)

	// Verify mtime matches UpdatedAt
	actualMtime := time.Unix(int64(out.Mtime), int64(out.Mtimensec))
	if !actualMtime.Equal(expectedMtime) {
		t.Errorf("mtime = %v, expected %v", actualMtime, expectedMtime)
	}

	// Verify ctime matches CreatedAt
	actualCtime := time.Unix(int64(out.Ctime), int64(out.Ctimensec))
	if !actualCtime.Equal(expectedCtime) {
		t.Errorf("ctime = %v, expected %v", actualCtime, expectedCtime)
	}
}

// TestIssueFileNode_Flush_TitleChange tests that Flush marks the cache dirty when title changes.
func TestIssueFileNode_Flush_TitleChange(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	issues := []cache.Issue{
		{Number: 1, Title: "Original Title", Body: "Original body", State: "open", Author: "testuser"},
	}
	populateTestIssues(t, db, repo, issues)

	var onDirtyCalled bool
	onDirty := func() { onDirtyCalled = true }

	fileNode := &issueFileNode{
		cache:   db,
		repo:    repo,
		number:  1,
		onDirty: onDirty,
	}

	ctx := context.Background()

	// Open and modify
	fh, _, errno := fileNode.Open(ctx, 0)
	if errno != 0 {
		t.Fatalf("Open returned error: %v", errno)
	}

	handle := fh.(*issueFileHandle)

	// Modify only the title in the buffer (leave body unchanged)
	content := string(handle.buffer)
	newContent := strings.Replace(content, "# Original Title", "# Updated Title", 1)
	handle.buffer = []byte(newContent)
	handle.dirty = true

	// Flush
	errno = fileNode.Flush(ctx, fh)
	if errno != 0 {
		t.Fatalf("Flush returned error: %v", errno)
	}

	// Verify onDirty callback was called
	if !onDirtyCalled {
		t.Error("onDirty callback should have been called for title change")
	}

	// Verify issue is marked dirty in cache
	issue, err := db.GetIssue(repo, 1)
	if err != nil {
		t.Fatalf("failed to get issue: %v", err)
	}
	if !issue.Dirty {
		t.Error("issue should be marked dirty in cache for title change")
	}
	if issue.Title != "Updated Title" {
		t.Errorf("issue title = %q, expected %q", issue.Title, "Updated Title")
	}
	// Body should remain unchanged
	if issue.Body != "Original body" {
		t.Errorf("issue body = %q, expected %q (unchanged)", issue.Body, "Original body")
	}
}

// TestIssueFileNode_Flush_TitleAndBodyChange tests that Flush handles both title and body changes.
func TestIssueFileNode_Flush_TitleAndBodyChange(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	issues := []cache.Issue{
		{Number: 1, Title: "Original Title", Body: "Original body", State: "open", Author: "testuser"},
	}
	populateTestIssues(t, db, repo, issues)

	var onDirtyCalled bool
	onDirty := func() { onDirtyCalled = true }

	fileNode := &issueFileNode{
		cache:   db,
		repo:    repo,
		number:  1,
		onDirty: onDirty,
	}

	ctx := context.Background()

	// Open and modify
	fh, _, errno := fileNode.Open(ctx, 0)
	if errno != 0 {
		t.Fatalf("Open returned error: %v", errno)
	}

	handle := fh.(*issueFileHandle)

	// Modify both title and body
	content := string(handle.buffer)
	newContent := strings.Replace(content, "# Original Title", "# New Title", 1)
	newContent = strings.Replace(newContent, "Original body", "New body content", 1)
	handle.buffer = []byte(newContent)
	handle.dirty = true

	// Flush
	errno = fileNode.Flush(ctx, fh)
	if errno != 0 {
		t.Fatalf("Flush returned error: %v", errno)
	}

	// Verify onDirty callback was called
	if !onDirtyCalled {
		t.Error("onDirty callback should have been called")
	}

	// Verify both title and body are updated in cache
	issue, err := db.GetIssue(repo, 1)
	if err != nil {
		t.Fatalf("failed to get issue: %v", err)
	}
	if !issue.Dirty {
		t.Error("issue should be marked dirty in cache")
	}
	if issue.Title != "New Title" {
		t.Errorf("issue title = %q, expected %q", issue.Title, "New Title")
	}
	if issue.Body != "New body content" {
		t.Errorf("issue body = %q, expected %q", issue.Body, "New body content")
	}
}

// TestIssueFileNode_Write_ExceedsMaxFileSize tests that writing beyond maxFileSize fails with EFBIG.
func TestIssueFileNode_Write_ExceedsMaxFileSize(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	issues := []cache.Issue{
		{Number: 1, Title: "Test Issue", Body: "Original body", State: "open"},
	}
	populateTestIssues(t, db, repo, issues)

	fileNode := &issueFileNode{
		cache:  db,
		repo:   repo,
		number: 1,
	}

	ctx := context.Background()

	// Open file
	fh, _, errno := fileNode.Open(ctx, 0)
	if errno != 0 {
		t.Fatalf("Open returned error: %v", errno)
	}

	// Try to write at an offset that would exceed maxFileSize (10MB)
	largeOffset := int64(maxFileSize - 100)
	data := make([]byte, 200) // This would result in end position > maxFileSize
	for i := range data {
		data[i] = 'X'
	}

	written, errno := fileNode.Write(ctx, fh, data, largeOffset)
	if errno != syscall.EFBIG {
		t.Errorf("Write beyond maxFileSize should return EFBIG, got errno=%v, written=%d", errno, written)
	}
	if written != 0 {
		t.Errorf("expected 0 bytes written on EFBIG error, got %d", written)
	}
}

// TestIssueFileNode_Write_AtMaxFileSize tests writing exactly up to maxFileSize succeeds.
func TestIssueFileNode_Write_AtMaxFileSize(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	issues := []cache.Issue{
		{Number: 1, Title: "Test", Body: "Short", State: "open"},
	}
	populateTestIssues(t, db, repo, issues)

	fileNode := &issueFileNode{
		cache:  db,
		repo:   repo,
		number: 1,
	}

	ctx := context.Background()

	// Open file
	fh, _, errno := fileNode.Open(ctx, 0)
	if errno != 0 {
		t.Fatalf("Open returned error: %v", errno)
	}

	// Write at offset that puts end position exactly at maxFileSize
	offset := int64(maxFileSize - 10)
	data := make([]byte, 10) // End position = maxFileSize exactly
	for i := range data {
		data[i] = 'Y'
	}

	written, errno := fileNode.Write(ctx, fh, data, offset)
	if errno != 0 {
		t.Errorf("Write up to maxFileSize should succeed, got errno=%v", errno)
	}
	if int(written) != len(data) {
		t.Errorf("expected %d bytes written, got %d", len(data), written)
	}
}

// TestIssueFileNode_Write_JustOverMaxFileSize tests writing 1 byte over maxFileSize fails.
func TestIssueFileNode_Write_JustOverMaxFileSize(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	issues := []cache.Issue{
		{Number: 1, Title: "Test", Body: "Short", State: "open"},
	}
	populateTestIssues(t, db, repo, issues)

	fileNode := &issueFileNode{
		cache:  db,
		repo:   repo,
		number: 1,
	}

	ctx := context.Background()

	// Open file
	fh, _, errno := fileNode.Open(ctx, 0)
	if errno != 0 {
		t.Fatalf("Open returned error: %v", errno)
	}

	// Write that puts end position exactly 1 byte over maxFileSize
	offset := int64(maxFileSize - 9)
	data := make([]byte, 10) // End position = maxFileSize + 1
	for i := range data {
		data[i] = 'Z'
	}

	written, errno := fileNode.Write(ctx, fh, data, offset)
	if errno != syscall.EFBIG {
		t.Errorf("Write 1 byte over maxFileSize should return EFBIG, got errno=%v", errno)
	}
	if written != 0 {
		t.Errorf("expected 0 bytes written on EFBIG error, got %d", written)
	}
}

// TestIssueFileNode_Flush_MalformedContent tests that flush returns EIO
// when the content cannot be parsed (broken template).
func TestIssueFileNode_Flush_MalformedContent(t *testing.T) {
	tests := []struct {
		name    string
		content string
	}{
		{
			name:    "missing frontmatter",
			content: "# Title\n\n## Body\n\nContent without frontmatter",
		},
		{
			name:    "malformed yaml - unclosed bracket",
			content: "---\nid: [invalid\nrepo: test/repo\n---\n\n# Title\n\n## Body\n\nContent",
		},
		{
			name:    "unclosed frontmatter",
			content: "---\nid: 1\nrepo: test/repo\n\n# Title\n\n## Body\n\nMissing closing delimiter",
		},
		{
			name:    "invalid id type in yaml",
			content: "---\nid: not_a_number\nrepo: test/repo\n---\n\n# Title\n\n## Body\n\nContent",
		},
		{
			name:    "frontmatter not at start",
			content: "\n---\nid: 1\nrepo: test/repo\n---\n\n# Title\n\n## Body\n\nContent",
		},
	}

	for i, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Create fresh database for each subtest to ensure isolation
			db, _ := setupTestCache(t)
			defer db.Close()

			repo := "test/repo"
			issueNum := 100 + i // Use unique issue number per test
			issues := []cache.Issue{
				{Number: issueNum, Title: "Test Issue", Body: "Original body", State: "open", Author: "testuser"},
			}
			populateTestIssues(t, db, repo, issues)

			var onDirtyCalled bool
			onDirty := func() { onDirtyCalled = true }

			fileNode := &issueFileNode{
				cache:   db,
				repo:    repo,
				number:  issueNum,
				onDirty: onDirty,
			}

			ctx := context.Background()

			// Open the file
			fh, _, errno := fileNode.Open(ctx, 0)
			if errno != 0 {
				t.Fatalf("Open returned error: %v", errno)
			}

			handle := fh.(*issueFileHandle)

			// Replace buffer with malformed content
			handle.buffer = []byte(tc.content)
			handle.dirty = true

			// Flush should return EIO
			errno = fileNode.Flush(ctx, fh)
			if errno != syscall.EIO {
				t.Errorf("Flush with %s should return EIO, got %v", tc.name, errno)
			}

			// Verify onDirty callback was NOT called
			if onDirtyCalled {
				t.Error("onDirty callback should not have been called on parse failure")
			}

			// Verify issue is NOT marked dirty in cache (still has original values)
			issue, err := db.GetIssue(repo, issueNum)
			if err != nil {
				t.Fatalf("failed to get issue: %v", err)
			}
			if issue.Dirty {
				t.Error("issue should NOT be marked dirty in cache after parse failure")
			}
			if issue.Body != "Original body" {
				t.Errorf("issue body should be unchanged, got %q", issue.Body)
			}
			if issue.Title != "Test Issue" {
				t.Errorf("issue title should be unchanged, got %q", issue.Title)
			}
		})
	}
}

// TestIssueFileNode_Flush_MalformedContent_PartiallyBroken tests flush with subtly broken content.
func TestIssueFileNode_Flush_MalformedContent_PartiallyBroken(t *testing.T) {
	db, _ := setupTestCache(t)
	defer db.Close()

	repo := "test/repo"
	issues := []cache.Issue{
		{Number: 1, Title: "Test Issue", Body: "Original body", State: "open", Author: "testuser"},
	}
	populateTestIssues(t, db, repo, issues)

	tests := []struct {
		name        string
		content     string
		shouldError bool
	}{
		{
			// Valid content should work
			name: "valid content",
			content: `---
id: 1
repo: test/repo
---

# Updated Title

## Body

Updated body content`,
			shouldError: false,
		},
		{
			// Missing ## Body section should still parse (body will be empty)
			name: "missing body section",
			content: `---
id: 1
repo: test/repo
---

# Title Without Body Section

Some content here but no ## Body section`,
			shouldError: false,
		},
		{
			// Extra whitespace in frontmatter should work
			name: "extra whitespace",
			content: `---
id: 1
repo: test/repo

---

# Title

## Body

Body content`,
			shouldError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			fileNode := &issueFileNode{
				cache:   db,
				repo:    repo,
				number:  1,
				onDirty: func() {},
			}

			ctx := context.Background()

			// Open the file
			fh, _, errno := fileNode.Open(ctx, 0)
			if errno != 0 {
				t.Fatalf("Open returned error: %v", errno)
			}

			handle := fh.(*issueFileHandle)

			// Replace buffer with test content
			handle.buffer = []byte(tc.content)
			handle.dirty = true

			// Flush
			errno = fileNode.Flush(ctx, fh)

			if tc.shouldError {
				if errno != syscall.EIO {
					t.Errorf("expected EIO, got %v", errno)
				}
			} else {
				if errno != 0 {
					t.Errorf("expected success, got error %v", errno)
				}
			}
		})
	}
}
