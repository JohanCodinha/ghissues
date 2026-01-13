//go:build integration

// Package integration contains end-to-end tests that require FUSE.
// Run with: go test -tags=integration ./internal/integration/...
package integration

import (
	"os"
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
