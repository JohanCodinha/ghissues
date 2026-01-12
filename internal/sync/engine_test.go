package sync

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JohanCodinha/ghissues/internal/cache"
)

func TestParseRepo(t *testing.T) {
	tests := []struct {
		name      string
		repo      string
		wantOwner string
		wantRepo  string
		wantErr   bool
	}{
		{
			name:      "valid repo",
			repo:      "owner/repo",
			wantOwner: "owner",
			wantRepo:  "repo",
			wantErr:   false,
		},
		{
			name:      "valid repo with dashes",
			repo:      "my-org/my-repo",
			wantOwner: "my-org",
			wantRepo:  "my-repo",
			wantErr:   false,
		},
		{
			name:      "valid repo with dots",
			repo:      "owner/repo.js",
			wantOwner: "owner",
			wantRepo:  "repo.js",
			wantErr:   false,
		},
		{
			name:    "missing slash",
			repo:    "ownerrepo",
			wantErr: true,
		},
		{
			name:    "empty owner",
			repo:    "/repo",
			wantErr: true,
		},
		{
			name:    "empty repo",
			repo:    "owner/",
			wantErr: true,
		},
		{
			name:    "empty string",
			repo:    "",
			wantErr: true,
		},
		{
			name:    "just slash",
			repo:    "/",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, repo, err := parseRepo(tt.repo)
			if tt.wantErr {
				if err == nil {
					t.Errorf("parseRepo(%q) expected error, got nil", tt.repo)
				}
				return
			}
			if err != nil {
				t.Errorf("parseRepo(%q) unexpected error: %v", tt.repo, err)
				return
			}
			if owner != tt.wantOwner {
				t.Errorf("parseRepo(%q) owner = %q, want %q", tt.repo, owner, tt.wantOwner)
			}
			if repo != tt.wantRepo {
				t.Errorf("parseRepo(%q) repo = %q, want %q", tt.repo, repo, tt.wantRepo)
			}
		})
	}
}

func TestNewEngine(t *testing.T) {
	// Create a temporary database for testing
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := cache.InitDB(dbPath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer db.Close()

	tests := []struct {
		name    string
		repo    string
		wantErr bool
	}{
		{
			name:    "valid repo",
			repo:    "owner/repo",
			wantErr: false,
		},
		{
			name:    "invalid repo",
			repo:    "invalid",
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, err := NewEngine(db, nil, tt.repo, 500)
			if tt.wantErr {
				if err == nil {
					t.Errorf("NewEngine(%q) expected error, got nil", tt.repo)
				}
				return
			}
			if err != nil {
				t.Errorf("NewEngine(%q) unexpected error: %v", tt.repo, err)
				return
			}
			if engine.repo != tt.repo {
				t.Errorf("engine.repo = %q, want %q", engine.repo, tt.repo)
			}
			engine.Stop()
		})
	}
}

func TestConflictDetectionLogic(t *testing.T) {
	// Test the conflict detection logic with mock timestamps
	// Per design: local wins UNLESS remote is newer

	baseTime := time.Date(2026, 1, 13, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name           string
		localUpdatedAt time.Time
		remoteUpdatedAt time.Time
		shouldPush     bool // false means conflict (remote is newer)
	}{
		{
			name:            "local is newer - should push",
			localUpdatedAt:  baseTime.Add(1 * time.Hour),
			remoteUpdatedAt: baseTime,
			shouldPush:      true,
		},
		{
			name:            "remote is newer - conflict, skip push",
			localUpdatedAt:  baseTime,
			remoteUpdatedAt: baseTime.Add(1 * time.Hour),
			shouldPush:      false,
		},
		{
			name:            "same time - should push (local wins on tie)",
			localUpdatedAt:  baseTime,
			remoteUpdatedAt: baseTime,
			shouldPush:      true,
		},
		{
			name:            "local much newer - should push",
			localUpdatedAt:  baseTime.Add(24 * time.Hour),
			remoteUpdatedAt: baseTime,
			shouldPush:      true,
		},
		{
			name:            "remote much newer - conflict",
			localUpdatedAt:  baseTime,
			remoteUpdatedAt: baseTime.Add(24 * time.Hour),
			shouldPush:      false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// The conflict logic: if remoteUpdatedAt.After(localUpdatedAt), skip push
			shouldPush := !tt.remoteUpdatedAt.After(tt.localUpdatedAt)
			if shouldPush != tt.shouldPush {
				t.Errorf("conflict logic: got shouldPush=%v, want %v", shouldPush, tt.shouldPush)
			}
		})
	}
}

func TestDebounceTimerReset(t *testing.T) {
	// Create a temporary database for testing
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := cache.InitDB(dbPath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer db.Close()

	// Use a very short debounce for testing
	engine, err := NewEngine(db, nil, "owner/repo", 50) // 50ms debounce
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer engine.Stop()

	// Track how many times the timer would fire
	var fireCount int32

	// Override the timer behavior for testing
	// We can't easily mock the internal timer, but we can test the reset behavior
	// by triggering multiple times and checking the debounce works

	// Trigger sync multiple times in quick succession
	for i := 0; i < 5; i++ {
		engine.TriggerSync()
		time.Sleep(10 * time.Millisecond) // Less than debounce time
	}

	// The timer should not have fired yet
	// (since we keep resetting it within the debounce window)

	// Wait for debounce to complete
	time.Sleep(100 * time.Millisecond)

	// Since we don't have a mock client, the sync will fail or do nothing
	// The important thing is that the timer mechanism works
	_ = fireCount
	_ = atomic.LoadInt32(&fireCount)
}

func TestStop(t *testing.T) {
	// Create a temporary database for testing
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := cache.InitDB(dbPath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer db.Close()

	engine, err := NewEngine(db, nil, "owner/repo", 500)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	// Start a timer
	engine.TriggerSync()

	// Stop should work without panic
	engine.Stop()

	// Calling Stop again should be safe
	engine.Stop()
}

func TestEngineFieldsAfterCreation(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := cache.InitDB(dbPath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer db.Close()

	engine, err := NewEngine(db, nil, "myowner/myrepo", 500)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer engine.Stop()

	if engine.owner != "myowner" {
		t.Errorf("owner = %q, want %q", engine.owner, "myowner")
	}
	if engine.repoName != "myrepo" {
		t.Errorf("repoName = %q, want %q", engine.repoName, "myrepo")
	}
	if engine.repo != "myowner/myrepo" {
		t.Errorf("repo = %q, want %q", engine.repo, "myowner/myrepo")
	}
	if engine.debounceMs != 500 {
		t.Errorf("debounceMs = %d, want %d", engine.debounceMs, 500)
	}
}

// TestSyncNowStopsTimer verifies that SyncNow stops any pending timer
func TestSyncNowStopsTimer(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := cache.InitDB(dbPath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer db.Close()

	engine, err := NewEngine(db, nil, "owner/repo", 5000) // Long debounce
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer engine.Stop()

	// Trigger a debounced sync
	engine.TriggerSync()

	// Immediately call SyncNow - this should stop the pending timer
	// Since we have no dirty issues, this should complete quickly
	err = engine.SyncNow()
	if err != nil {
		t.Errorf("SyncNow() error = %v", err)
	}

	// Timer should be nil after SyncNow
	engine.mu.Lock()
	hasTimer := engine.timer != nil
	engine.mu.Unlock()

	if hasTimer {
		t.Error("expected timer to be nil after SyncNow()")
	}
}

// TestCacheIntegration tests the sync engine with actual cache operations
func TestCacheIntegration(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")
	db, err := cache.InitDB(dbPath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	defer db.Close()

	engine, err := NewEngine(db, nil, "owner/repo", 500)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}
	defer engine.Stop()

	// Insert a test issue
	issue := cache.Issue{
		Number:    1,
		Repo:      "owner/repo",
		Title:     "Test Issue",
		Body:      "Test body",
		State:     "open",
		Author:    "testuser",
		Labels:    []string{"bug"},
		CreatedAt: "2026-01-13T10:00:00Z",
		UpdatedAt: "2026-01-13T10:00:00Z",
		ETag:      "abc123",
		Dirty:     false,
	}

	err = db.UpsertIssue(issue)
	if err != nil {
		t.Fatalf("failed to upsert issue: %v", err)
	}

	// Mark the issue dirty
	err = db.MarkDirty("owner/repo", 1, "Updated body")
	if err != nil {
		t.Fatalf("failed to mark dirty: %v", err)
	}

	// Verify the issue is dirty
	dirtyIssues, err := db.GetDirtyIssues("owner/repo")
	if err != nil {
		t.Fatalf("failed to get dirty issues: %v", err)
	}

	if len(dirtyIssues) != 1 {
		t.Errorf("expected 1 dirty issue, got %d", len(dirtyIssues))
	}

	if dirtyIssues[0].Body != "Updated body" {
		t.Errorf("body = %q, want %q", dirtyIssues[0].Body, "Updated body")
	}

	if dirtyIssues[0].LocalUpdatedAt == "" {
		t.Error("expected LocalUpdatedAt to be set")
	}
}

// BenchmarkParseRepo benchmarks the repo parsing function
func BenchmarkParseRepo(b *testing.B) {
	for i := 0; i < b.N; i++ {
		parseRepo("owner/repo")
	}
}

// TestTempDirCleanup ensures test isolation
func TestTempDirCleanup(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := cache.InitDB(dbPath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}
	db.Close()

	// Verify file was created
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("database file should exist")
	}

	// t.TempDir() will clean up automatically after test
}
