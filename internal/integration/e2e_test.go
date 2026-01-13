//go:build integration

// Package integration contains end-to-end tests that require FUSE.
// Run with: go test -tags=integration ./internal/integration/...
package integration

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JohanCodinha/ghissues/internal/cache"
	"github.com/JohanCodinha/ghissues/internal/fs"
	"github.com/JohanCodinha/ghissues/internal/gh"
	"github.com/JohanCodinha/ghissues/internal/sync"
)

// TestE2E_MountReadWrite tests the full mount → read → write → sync cycle
func TestE2E_MountReadWrite(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("FUSE tests require root or CAP_SYS_ADMIN")
	}

	// Start mock GitHub server
	mockGH := gh.NewMockServer()
	defer mockGH.Close()

	// Seed test data
	mockGH.AddIssue(&gh.Issue{
		Number:    1,
		Title:     "Test Issue",
		Body:      "Original body content",
		State:     "open",
		User:      gh.User{Login: "testuser"},
		CreatedAt: time.Now().Add(-24 * time.Hour),
		UpdatedAt: time.Now().Add(-1 * time.Hour),
		ETag:      `"initial-etag"`,
	})

	// Create temp directories
	tmpDir := t.TempDir()
	mountpoint := filepath.Join(tmpDir, "mount")
	cachePath := filepath.Join(tmpDir, "cache.db")

	if err := os.MkdirAll(mountpoint, 0755); err != nil {
		t.Fatalf("failed to create mountpoint: %v", err)
	}

	// Initialize components
	cacheDB, err := cache.InitDB(cachePath)
	if err != nil {
		t.Fatalf("failed to init cache: %v", err)
	}
	defer cacheDB.Close()

	client := gh.NewWithBaseURL("test-token", mockGH.URL)
	repo := "testowner/testrepo"

	engine, err := sync.NewEngine(cacheDB, client, repo, 100) // 100ms debounce for tests
	if err != nil {
		t.Fatalf("failed to create sync engine: %v", err)
	}
	defer engine.Stop()

	// Initial sync
	if err := engine.InitialSync(); err != nil {
		t.Fatalf("initial sync failed: %v", err)
	}

	// Create and mount filesystem
	filesystem := fs.NewFS(cacheDB, repo, mountpoint, func() {
		engine.TriggerSync()
	})

	// Mount in goroutine (blocks until unmount)
	mountErr := make(chan error, 1)
	go func() {
		mountErr <- filesystem.Mount()
	}()

	// Wait for mount
	time.Sleep(500 * time.Millisecond)

	// Test 1: List files
	t.Run("ListFiles", func(t *testing.T) {
		entries, err := os.ReadDir(mountpoint)
		if err != nil {
			t.Fatalf("failed to read mountpoint: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 file, got %d", len(entries))
		}
		if !strings.Contains(entries[0].Name(), "[1].md") {
			t.Errorf("expected filename to contain [1].md, got %s", entries[0].Name())
		}
	})

	// Test 2: Read file
	t.Run("ReadFile", func(t *testing.T) {
		files, _ := os.ReadDir(mountpoint)
		content, err := os.ReadFile(filepath.Join(mountpoint, files[0].Name()))
		if err != nil {
			t.Fatalf("failed to read file: %v", err)
		}
		if !strings.Contains(string(content), "Original body content") {
			t.Errorf("expected body content, got: %s", string(content))
		}
		if !strings.Contains(string(content), "# Test Issue") {
			t.Errorf("expected title, got: %s", string(content))
		}
	})

	// Test 3: Write file and verify sync
	t.Run("WriteAndSync", func(t *testing.T) {
		files, _ := os.ReadDir(mountpoint)
		filePath := filepath.Join(mountpoint, files[0].Name())

		// Read current content
		content, _ := os.ReadFile(filePath)

		// Modify body
		newContent := strings.Replace(
			string(content),
			"Original body content",
			"Modified body content",
			1,
		)

		// Write back
		if err := os.WriteFile(filePath, []byte(newContent), 0644); err != nil {
			t.Fatalf("failed to write file: %v", err)
		}

		// Wait for debounced sync
		time.Sleep(300 * time.Millisecond)

		// Verify mock server received update
		issue := mockGH.GetIssue(1)
		if issue == nil {
			t.Fatal("issue not found in mock server")
		}
		if !strings.Contains(issue.Body, "Modified body content") {
			t.Errorf("sync did not update issue body: %s", issue.Body)
		}
	})

	// Unmount
	if err := filesystem.Unmount(); err != nil {
		t.Logf("unmount warning: %v", err)
	}

	select {
	case err := <-mountErr:
		if err != nil {
			t.Logf("mount returned: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Log("mount did not return in time")
	}
}

// TestE2E_IssueWithComments tests that comments are correctly rendered
func TestE2E_IssueWithComments(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("FUSE tests require root or CAP_SYS_ADMIN")
	}

	// Start mock GitHub server
	mockGH := gh.NewMockServer()
	defer mockGH.Close()

	// Seed test data with an issue
	mockGH.AddIssue(&gh.Issue{
		Number:    5,
		Title:     "Issue with Comments",
		Body:      "This issue has comments",
		State:     "open",
		User:      gh.User{Login: "issueauthor"},
		CreatedAt: time.Now().Add(-48 * time.Hour),
		UpdatedAt: time.Now().Add(-1 * time.Hour),
		ETag:      `"comments-test-etag"`,
	})

	// Add comments to the issue
	mockGH.AddComment(5, &gh.Comment{
		ID:        101,
		User:      gh.User{Login: "commenter1"},
		Body:      "This is the first comment",
		CreatedAt: time.Now().Add(-24 * time.Hour),
		UpdatedAt: time.Now().Add(-24 * time.Hour),
	})
	mockGH.AddComment(5, &gh.Comment{
		ID:        102,
		User:      gh.User{Login: "commenter2"},
		Body:      "This is the second comment with more detail",
		CreatedAt: time.Now().Add(-12 * time.Hour),
		UpdatedAt: time.Now().Add(-12 * time.Hour),
	})

	// Create temp directories
	tmpDir := t.TempDir()
	mountpoint := filepath.Join(tmpDir, "mount")
	cachePath := filepath.Join(tmpDir, "cache.db")

	if err := os.MkdirAll(mountpoint, 0755); err != nil {
		t.Fatalf("failed to create mountpoint: %v", err)
	}

	// Initialize components
	cacheDB, err := cache.InitDB(cachePath)
	if err != nil {
		t.Fatalf("failed to init cache: %v", err)
	}
	defer cacheDB.Close()

	client := gh.NewWithBaseURL("test-token", mockGH.URL)
	repo := "testowner/testrepo"

	engine, err := sync.NewEngine(cacheDB, client, repo, 100) // 100ms debounce for tests
	if err != nil {
		t.Fatalf("failed to create sync engine: %v", err)
	}
	defer engine.Stop()

	// Initial sync
	if err := engine.InitialSync(); err != nil {
		t.Fatalf("initial sync failed: %v", err)
	}

	// Create and mount filesystem
	filesystem := fs.NewFS(cacheDB, repo, mountpoint, func() {
		engine.TriggerSync()
	})

	// Mount in goroutine (blocks until unmount)
	mountErr := make(chan error, 1)
	go func() {
		mountErr <- filesystem.Mount()
	}()

	// Wait for mount
	time.Sleep(500 * time.Millisecond)

	// Test: Read file with comments
	t.Run("ReadFileWithComments", func(t *testing.T) {
		files, _ := os.ReadDir(mountpoint)
		var issueFile string
		for _, f := range files {
			if strings.Contains(f.Name(), "[5].md") {
				issueFile = f.Name()
				break
			}
		}
		if issueFile == "" {
			t.Fatalf("could not find issue file for #5")
		}

		content, err := os.ReadFile(filepath.Join(mountpoint, issueFile))
		if err != nil {
			t.Fatalf("failed to read file: %v", err)
		}

		contentStr := string(content)

		// Verify frontmatter contains comments count
		if !strings.Contains(contentStr, "comments: 2") {
			t.Errorf("expected 'comments: 2' in frontmatter, got: %s", contentStr)
		}

		// Verify ## Comments section exists
		if !strings.Contains(contentStr, "## Comments") {
			t.Errorf("expected '## Comments' section, got: %s", contentStr)
		}

		// Verify first comment author and body appear
		if !strings.Contains(contentStr, "commenter1") {
			t.Errorf("expected comment author 'commenter1', got: %s", contentStr)
		}
		if !strings.Contains(contentStr, "This is the first comment") {
			t.Errorf("expected first comment body, got: %s", contentStr)
		}

		// Verify second comment author and body appear
		if !strings.Contains(contentStr, "commenter2") {
			t.Errorf("expected comment author 'commenter2', got: %s", contentStr)
		}
		if !strings.Contains(contentStr, "This is the second comment with more detail") {
			t.Errorf("expected second comment body, got: %s", contentStr)
		}
	})

	// Unmount
	if err := filesystem.Unmount(); err != nil {
		t.Logf("unmount warning: %v", err)
	}

	select {
	case err := <-mountErr:
		if err != nil {
			t.Logf("mount returned: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Log("mount did not return in time")
	}
}

// TestE2E_OfflineMode tests that reads work when GitHub is unavailable
func TestE2E_OfflineMode(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("FUSE tests require root or CAP_SYS_ADMIN")
	}

	// Start mock server, seed data, then shut it down
	mockGH := gh.NewMockServer()
	mockGH.AddIssue(&gh.Issue{
		Number:    42,
		Title:     "Offline Test",
		Body:      "This should be cached",
		State:     "open",
		User:      gh.User{Login: "offlineuser"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		ETag:      `"offline-etag"`,
	})

	tmpDir := t.TempDir()
	mountpoint := filepath.Join(tmpDir, "mount")
	cachePath := filepath.Join(tmpDir, "cache.db")
	os.MkdirAll(mountpoint, 0755)

	// Initialize and sync while online
	cacheDB, _ := cache.InitDB(cachePath)
	client := gh.NewWithBaseURL("test-token", mockGH.URL)
	engine, _ := sync.NewEngine(cacheDB, client, "test/repo", 100)
	engine.InitialSync()

	// Shut down mock server (simulate offline)
	mockGH.Close()

	// Mount should still work (serves from cache)
	filesystem := fs.NewFS(cacheDB, "test/repo", mountpoint, func() {
		engine.TriggerSync()
	})

	go filesystem.Mount()
	time.Sleep(500 * time.Millisecond)

	// Should still be able to read from cache
	entries, err := os.ReadDir(mountpoint)
	if err != nil {
		t.Fatalf("failed to read mountpoint offline: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 cached file, got %d", len(entries))
	}

	content, _ := os.ReadFile(filepath.Join(mountpoint, entries[0].Name()))
	if !strings.Contains(string(content), "This should be cached") {
		t.Error("cached content not available offline")
	}

	filesystem.Unmount()
	engine.Stop()
	cacheDB.Close()
}

// TestE2E_LargeIssueWithManyComments tests handling of large issues with many comments
func TestE2E_LargeIssueWithManyComments(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("FUSE tests require root or CAP_SYS_ADMIN")
	}

	mockGH := gh.NewMockServer()
	defer mockGH.Close()

	// Create a ~10KB body
	largeBody := strings.Repeat("This is a line of content for the issue body. ", 200) // ~10KB

	mockGH.AddIssue(&gh.Issue{
		Number:    1,
		Title:     "Large Issue with Many Comments",
		Body:      largeBody,
		State:     "open",
		User:      gh.User{Login: "testuser"},
		CreatedAt: time.Now().Add(-48 * time.Hour),
		UpdatedAt: time.Now().Add(-1 * time.Hour),
		ETag:      `"large-issue-etag"`,
	})

	// Add 50 comments
	for i := 1; i <= 50; i++ {
		mockGH.AddComment(1, &gh.Comment{
			ID:        int64(i),
			User:      gh.User{Login: fmt.Sprintf("commenter%d", i)},
			Body:      fmt.Sprintf("This is comment number %d with some content.", i),
			CreatedAt: time.Now().Add(-time.Duration(50-i) * time.Hour),
			UpdatedAt: time.Now().Add(-time.Duration(50-i) * time.Hour),
		})
	}

	tmpDir := t.TempDir()
	mountpoint := filepath.Join(tmpDir, "mount")
	cachePath := filepath.Join(tmpDir, "cache.db")
	os.MkdirAll(mountpoint, 0755)

	cacheDB, err := cache.InitDB(cachePath)
	if err != nil {
		t.Fatalf("failed to init cache: %v", err)
	}
	defer cacheDB.Close()

	client := gh.NewWithBaseURL("test-token", mockGH.URL)
	engine, err := sync.NewEngine(cacheDB, client, "test/repo", 100)
	if err != nil {
		t.Fatalf("failed to create sync engine: %v", err)
	}
	defer engine.Stop()

	if err := engine.InitialSync(); err != nil {
		t.Fatalf("initial sync failed: %v", err)
	}

	filesystem := fs.NewFS(cacheDB, "test/repo", mountpoint, func() {
		engine.TriggerSync()
	})

	go filesystem.Mount()
	time.Sleep(500 * time.Millisecond)
	defer filesystem.Unmount()

	t.Run("FileIsReadable", func(t *testing.T) {
		entries, err := os.ReadDir(mountpoint)
		if err != nil {
			t.Fatalf("failed to read mountpoint: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 file, got %d", len(entries))
		}

		content, err := os.ReadFile(filepath.Join(mountpoint, entries[0].Name()))
		if err != nil {
			t.Fatalf("failed to read file: %v", err)
		}

		// File should be readable and substantial in size
		if len(content) < 10000 {
			t.Errorf("expected file to be at least 10KB, got %d bytes", len(content))
		}
	})

	t.Run("AllCommentsPresent", func(t *testing.T) {
		entries, _ := os.ReadDir(mountpoint)
		content, _ := os.ReadFile(filepath.Join(mountpoint, entries[0].Name()))
		contentStr := string(content)

		// Verify comments count in frontmatter
		if !strings.Contains(contentStr, "comments: 50") {
			t.Errorf("expected 'comments: 50' in frontmatter")
		}

		// Verify some sample comments are present
		for _, num := range []int{1, 25, 50} {
			expected := fmt.Sprintf("This is comment number %d", num)
			if !strings.Contains(contentStr, expected) {
				t.Errorf("expected to find comment %d, not found", num)
			}
		}
	})

	t.Run("PartialRead", func(t *testing.T) {
		entries, _ := os.ReadDir(mountpoint)
		filePath := filepath.Join(mountpoint, entries[0].Name())

		// Open file and read only first 1KB
		f, err := os.Open(filePath)
		if err != nil {
			t.Fatalf("failed to open file: %v", err)
		}
		defer f.Close()

		buf := make([]byte, 1024)
		n, err := f.Read(buf)
		if err != nil {
			t.Fatalf("failed to read partial content: %v", err)
		}

		if n != 1024 {
			t.Errorf("expected to read 1024 bytes, got %d", n)
		}

		// First 1KB should contain the frontmatter
		if !strings.Contains(string(buf), "---") {
			t.Error("partial read should contain frontmatter marker")
		}
	})
}

// TestE2E_SimilarTitles tests that issues with similar titles are correctly distinguished
func TestE2E_SimilarTitles(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("FUSE tests require root or CAP_SYS_ADMIN")
	}

	mockGH := gh.NewMockServer()
	defer mockGH.Close()

	// Create 3 issues with similar titles
	mockGH.AddIssue(&gh.Issue{
		Number:    1,
		Title:     "Bug in login",
		Body:      "Issue one body content",
		State:     "open",
		User:      gh.User{Login: "user1"},
		CreatedAt: time.Now().Add(-3 * time.Hour),
		UpdatedAt: time.Now().Add(-3 * time.Hour),
		ETag:      `"similar-1"`,
	})
	mockGH.AddIssue(&gh.Issue{
		Number:    2,
		Title:     "Bug in login flow",
		Body:      "Issue two body content",
		State:     "open",
		User:      gh.User{Login: "user2"},
		CreatedAt: time.Now().Add(-2 * time.Hour),
		UpdatedAt: time.Now().Add(-2 * time.Hour),
		ETag:      `"similar-2"`,
	})
	mockGH.AddIssue(&gh.Issue{
		Number:    3,
		Title:     "Bug in login page",
		Body:      "Issue three body content",
		State:     "open",
		User:      gh.User{Login: "user3"},
		CreatedAt: time.Now().Add(-1 * time.Hour),
		UpdatedAt: time.Now().Add(-1 * time.Hour),
		ETag:      `"similar-3"`,
	})

	tmpDir := t.TempDir()
	mountpoint := filepath.Join(tmpDir, "mount")
	cachePath := filepath.Join(tmpDir, "cache.db")
	os.MkdirAll(mountpoint, 0755)

	cacheDB, err := cache.InitDB(cachePath)
	if err != nil {
		t.Fatalf("failed to init cache: %v", err)
	}
	defer cacheDB.Close()

	client := gh.NewWithBaseURL("test-token", mockGH.URL)
	engine, err := sync.NewEngine(cacheDB, client, "test/repo", 100)
	if err != nil {
		t.Fatalf("failed to create sync engine: %v", err)
	}
	defer engine.Stop()

	if err := engine.InitialSync(); err != nil {
		t.Fatalf("initial sync failed: %v", err)
	}

	filesystem := fs.NewFS(cacheDB, "test/repo", mountpoint, func() {
		engine.TriggerSync()
	})

	go filesystem.Mount()
	time.Sleep(500 * time.Millisecond)
	defer filesystem.Unmount()

	t.Run("AllThreeFilesListed", func(t *testing.T) {
		entries, err := os.ReadDir(mountpoint)
		if err != nil {
			t.Fatalf("failed to read mountpoint: %v", err)
		}
		if len(entries) != 3 {
			t.Fatalf("expected 3 files, got %d", len(entries))
		}
	})

	t.Run("EachFileHasCorrectContent", func(t *testing.T) {
		entries, _ := os.ReadDir(mountpoint)

		// Map issue numbers to expected body content
		expectedContent := map[int]string{
			1: "Issue one body content",
			2: "Issue two body content",
			3: "Issue three body content",
		}

		for _, entry := range entries {
			// Extract issue number from filename
			var issueNum int
			for num := 1; num <= 3; num++ {
				if strings.Contains(entry.Name(), fmt.Sprintf("[%d].md", num)) {
					issueNum = num
					break
				}
			}

			if issueNum == 0 {
				t.Errorf("could not extract issue number from filename: %s", entry.Name())
				continue
			}

			content, err := os.ReadFile(filepath.Join(mountpoint, entry.Name()))
			if err != nil {
				t.Errorf("failed to read file %s: %v", entry.Name(), err)
				continue
			}

			expectedBody := expectedContent[issueNum]
			if !strings.Contains(string(content), expectedBody) {
				t.Errorf("file %s should contain '%s', got: %s", entry.Name(), expectedBody, string(content))
			}
		}
	})
}

// TestE2E_EmptyIssue tests handling of issues with empty body
func TestE2E_EmptyIssue(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("FUSE tests require root or CAP_SYS_ADMIN")
	}

	mockGH := gh.NewMockServer()
	defer mockGH.Close()

	// Create issue with empty body
	mockGH.AddIssue(&gh.Issue{
		Number:    1,
		Title:     "Empty Body Issue",
		Body:      "",
		State:     "open",
		User:      gh.User{Login: "testuser"},
		CreatedAt: time.Now().Add(-1 * time.Hour),
		UpdatedAt: time.Now().Add(-1 * time.Hour),
		ETag:      `"empty-body-etag"`,
	})

	tmpDir := t.TempDir()
	mountpoint := filepath.Join(tmpDir, "mount")
	cachePath := filepath.Join(tmpDir, "cache.db")
	os.MkdirAll(mountpoint, 0755)

	cacheDB, err := cache.InitDB(cachePath)
	if err != nil {
		t.Fatalf("failed to init cache: %v", err)
	}
	defer cacheDB.Close()

	client := gh.NewWithBaseURL("test-token", mockGH.URL)
	engine, err := sync.NewEngine(cacheDB, client, "test/repo", 100)
	if err != nil {
		t.Fatalf("failed to create sync engine: %v", err)
	}
	defer engine.Stop()

	if err := engine.InitialSync(); err != nil {
		t.Fatalf("initial sync failed: %v", err)
	}

	filesystem := fs.NewFS(cacheDB, "test/repo", mountpoint, func() {
		engine.TriggerSync()
	})

	go filesystem.Mount()
	time.Sleep(500 * time.Millisecond)
	defer filesystem.Unmount()

	t.Run("FileIsReadable", func(t *testing.T) {
		entries, err := os.ReadDir(mountpoint)
		if err != nil {
			t.Fatalf("failed to read mountpoint: %v", err)
		}
		if len(entries) != 1 {
			t.Fatalf("expected 1 file, got %d", len(entries))
		}

		content, err := os.ReadFile(filepath.Join(mountpoint, entries[0].Name()))
		if err != nil {
			t.Fatalf("failed to read file: %v", err)
		}

		// Should still have frontmatter and title
		if !strings.Contains(string(content), "# Empty Body Issue") {
			t.Error("file should contain issue title")
		}
	})

	t.Run("CanWriteContentToEmptyBody", func(t *testing.T) {
		entries, _ := os.ReadDir(mountpoint)
		filePath := filepath.Join(mountpoint, entries[0].Name())

		// Read current content
		content, _ := os.ReadFile(filePath)
		contentStr := string(content)

		// Find where the body section would be (after frontmatter and title)
		// Add content after the title
		newContent := strings.Replace(contentStr, "# Empty Body Issue", "# Empty Body Issue\n\nNew body content added here.", 1)

		// Write back
		if err := os.WriteFile(filePath, []byte(newContent), 0644); err != nil {
			t.Fatalf("failed to write file: %v", err)
		}

		// Wait for sync
		time.Sleep(300 * time.Millisecond)

		// Verify mock server received update
		issue := mockGH.GetIssue(1)
		if issue == nil {
			t.Fatal("issue not found in mock server")
		}
		if !strings.Contains(issue.Body, "New body content added here") {
			t.Errorf("sync did not update issue body: %s", issue.Body)
		}
	})
}

// TestE2E_GrepAcrossFiles tests using grep command on mounted files
func TestE2E_GrepAcrossFiles(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("FUSE tests require root or CAP_SYS_ADMIN")
	}

	mockGH := gh.NewMockServer()
	defer mockGH.Close()

	// Create 3 issues, 2 containing "SEARCH_TERM", 1 without
	mockGH.AddIssue(&gh.Issue{
		Number:    1,
		Title:     "Issue with search term",
		Body:      "This issue contains SEARCH_TERM in the body",
		State:     "open",
		User:      gh.User{Login: "user1"},
		CreatedAt: time.Now().Add(-3 * time.Hour),
		UpdatedAt: time.Now().Add(-3 * time.Hour),
		ETag:      `"grep-1"`,
	})
	mockGH.AddIssue(&gh.Issue{
		Number:    2,
		Title:     "Issue without the term",
		Body:      "This issue does not contain it",
		State:     "open",
		User:      gh.User{Login: "user2"},
		CreatedAt: time.Now().Add(-2 * time.Hour),
		UpdatedAt: time.Now().Add(-2 * time.Hour),
		ETag:      `"grep-2"`,
	})
	mockGH.AddIssue(&gh.Issue{
		Number:    3,
		Title:     "Another with search term",
		Body:      "Also has SEARCH_TERM in the content",
		State:     "open",
		User:      gh.User{Login: "user3"},
		CreatedAt: time.Now().Add(-1 * time.Hour),
		UpdatedAt: time.Now().Add(-1 * time.Hour),
		ETag:      `"grep-3"`,
	})

	tmpDir := t.TempDir()
	mountpoint := filepath.Join(tmpDir, "mount")
	cachePath := filepath.Join(tmpDir, "cache.db")
	os.MkdirAll(mountpoint, 0755)

	cacheDB, err := cache.InitDB(cachePath)
	if err != nil {
		t.Fatalf("failed to init cache: %v", err)
	}
	defer cacheDB.Close()

	client := gh.NewWithBaseURL("test-token", mockGH.URL)
	engine, err := sync.NewEngine(cacheDB, client, "test/repo", 100)
	if err != nil {
		t.Fatalf("failed to create sync engine: %v", err)
	}
	defer engine.Stop()

	if err := engine.InitialSync(); err != nil {
		t.Fatalf("initial sync failed: %v", err)
	}

	filesystem := fs.NewFS(cacheDB, "test/repo", mountpoint, func() {
		engine.TriggerSync()
	})

	go filesystem.Mount()
	time.Sleep(500 * time.Millisecond)
	defer filesystem.Unmount()

	t.Run("GrepFindsExactlyTwoFiles", func(t *testing.T) {
		// Use shell to expand glob pattern
		cmd := exec.Command("sh", "-c", fmt.Sprintf("grep -l SEARCH_TERM %s/*.md", mountpoint))
		output, err := cmd.Output()
		if err != nil {
			// grep returns exit code 1 if no matches, but we expect matches
			if exitErr, ok := err.(*exec.ExitError); ok && exitErr.ExitCode() != 1 {
				t.Fatalf("grep command failed: %v, stderr: %s", err, exitErr.Stderr)
			}
		}

		// Count the number of files found
		outputStr := strings.TrimSpace(string(output))
		if outputStr == "" {
			t.Fatal("grep found no files")
		}

		files := strings.Split(outputStr, "\n")
		if len(files) != 2 {
			t.Errorf("expected grep to find 2 files, found %d: %v", len(files), files)
		}
	})
}

// TestE2E_FindCommand tests using find command on mounted filesystem
func TestE2E_FindCommand(t *testing.T) {
	if os.Getuid() != 0 {
		t.Skip("FUSE tests require root or CAP_SYS_ADMIN")
	}

	mockGH := gh.NewMockServer()
	defer mockGH.Close()

	// Create 3 issues
	mockGH.AddIssue(&gh.Issue{
		Number:    1,
		Title:     "First Issue",
		Body:      "First issue body",
		State:     "open",
		User:      gh.User{Login: "user1"},
		CreatedAt: time.Now().Add(-3 * time.Hour),
		UpdatedAt: time.Now().Add(-3 * time.Hour),
		ETag:      `"find-1"`,
	})
	mockGH.AddIssue(&gh.Issue{
		Number:    2,
		Title:     "Second Issue",
		Body:      "Second issue body",
		State:     "open",
		User:      gh.User{Login: "user2"},
		CreatedAt: time.Now().Add(-2 * time.Hour),
		UpdatedAt: time.Now().Add(-2 * time.Hour),
		ETag:      `"find-2"`,
	})
	mockGH.AddIssue(&gh.Issue{
		Number:    3,
		Title:     "Third Issue",
		Body:      "Third issue body",
		State:     "open",
		User:      gh.User{Login: "user3"},
		CreatedAt: time.Now().Add(-1 * time.Hour),
		UpdatedAt: time.Now().Add(-1 * time.Hour),
		ETag:      `"find-3"`,
	})

	tmpDir := t.TempDir()
	mountpoint := filepath.Join(tmpDir, "mount")
	cachePath := filepath.Join(tmpDir, "cache.db")
	os.MkdirAll(mountpoint, 0755)

	cacheDB, err := cache.InitDB(cachePath)
	if err != nil {
		t.Fatalf("failed to init cache: %v", err)
	}
	defer cacheDB.Close()

	client := gh.NewWithBaseURL("test-token", mockGH.URL)
	engine, err := sync.NewEngine(cacheDB, client, "test/repo", 100)
	if err != nil {
		t.Fatalf("failed to create sync engine: %v", err)
	}
	defer engine.Stop()

	if err := engine.InitialSync(); err != nil {
		t.Fatalf("initial sync failed: %v", err)
	}

	filesystem := fs.NewFS(cacheDB, "test/repo", mountpoint, func() {
		engine.TriggerSync()
	})

	go filesystem.Mount()
	time.Sleep(500 * time.Millisecond)
	defer filesystem.Unmount()

	t.Run("FindReturnsThreeFiles", func(t *testing.T) {
		cmd := exec.Command("find", mountpoint, "-name", "*.md")
		output, err := cmd.Output()
		if err != nil {
			t.Fatalf("find command failed: %v", err)
		}

		outputStr := strings.TrimSpace(string(output))
		if outputStr == "" {
			t.Fatal("find found no files")
		}

		files := strings.Split(outputStr, "\n")
		if len(files) != 3 {
			t.Errorf("expected find to return 3 files, got %d: %v", len(files), files)
		}

		// Verify all files end with .md
		for _, file := range files {
			if !strings.HasSuffix(file, ".md") {
				t.Errorf("expected file to end with .md, got: %s", file)
			}
		}
	})
}
