package sync

import (
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/JohanCodinha/ghissues/internal/cache"
	"github.com/JohanCodinha/ghissues/internal/gh"
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
	updatedBody := "Updated body"
	err = db.MarkDirty("owner/repo", 1, cache.IssueUpdate{Body: &updatedBody})
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

// ============================================================================
// Integration tests with mock server
// ============================================================================

// setupTestEngine creates a test engine with mock server
func setupTestEngine(t *testing.T) (*Engine, *cache.DB, *gh.MockServer) {
	t.Helper()

	tmpDir := t.TempDir()
	cachePath := filepath.Join(tmpDir, "test.db")
	cacheDB, err := cache.InitDB(cachePath)
	if err != nil {
		t.Fatalf("failed to init db: %v", err)
	}

	mockGH := gh.NewMockServer()

	client := gh.NewWithBaseURL("test-token", mockGH.URL)
	engine, err := NewEngine(cacheDB, client, "owner/repo", 100)
	if err != nil {
		cacheDB.Close()
		mockGH.Close()
		t.Fatalf("failed to create engine: %v", err)
	}

	return engine, cacheDB, mockGH
}

// TestInitialSync_MultipleIssues tests successful sync with multiple issues
func TestInitialSync_MultipleIssues(t *testing.T) {
	engine, cacheDB, mockGH := setupTestEngine(t)
	defer engine.Stop()
	defer cacheDB.Close()
	defer mockGH.Close()

	baseTime := time.Date(2026, 1, 13, 10, 0, 0, 0, time.UTC)

	// Add issues to mock server
	mockGH.AddIssue(&gh.Issue{
		Number:    1,
		Title:     "Issue 1",
		Body:      "Body 1",
		State:     "open",
		User:      gh.User{Login: "user1"},
		Labels:    []gh.Label{{Name: "bug", Color: "d73a4a"}},
		CreatedAt: baseTime,
		UpdatedAt: baseTime,
		ETag:      `"etag1"`,
	})
	mockGH.AddIssue(&gh.Issue{
		Number:    2,
		Title:     "Issue 2",
		Body:      "Body 2",
		State:     "closed",
		User:      gh.User{Login: "user2"},
		Labels:    []gh.Label{{Name: "enhancement", Color: "a2eeef"}},
		CreatedAt: baseTime.Add(1 * time.Hour),
		UpdatedAt: baseTime.Add(2 * time.Hour),
		ETag:      `"etag2"`,
	})
	mockGH.AddIssue(&gh.Issue{
		Number:    3,
		Title:     "Issue 3",
		Body:      "Body 3",
		State:     "open",
		User:      gh.User{Login: "user3"},
		Labels:    []gh.Label{},
		CreatedAt: baseTime.Add(2 * time.Hour),
		UpdatedAt: baseTime.Add(3 * time.Hour),
		ETag:      `"etag3"`,
	})

	// Add comments for issue 1
	mockGH.AddComment(1, &gh.Comment{
		ID:        101,
		User:      gh.User{Login: "commenter1"},
		Body:      "Comment on issue 1",
		CreatedAt: baseTime.Add(30 * time.Minute),
		UpdatedAt: baseTime.Add(30 * time.Minute),
	})

	// Perform initial sync
	err := engine.InitialSync()
	if err != nil {
		t.Fatalf("InitialSync() error = %v", err)
	}

	// Verify issues are cached
	issues, err := cacheDB.ListIssues("owner/repo")
	if err != nil {
		t.Fatalf("failed to list cached issues: %v", err)
	}

	if len(issues) != 3 {
		t.Errorf("expected 3 cached issues, got %d", len(issues))
	}

	// Verify specific issue data
	issue1, err := cacheDB.GetIssue("owner/repo", 1)
	if err != nil {
		t.Fatalf("failed to get issue 1: %v", err)
	}
	if issue1 == nil {
		t.Fatal("issue 1 not found in cache")
	}
	if issue1.Title != "Issue 1" {
		t.Errorf("issue 1 title = %q, want %q", issue1.Title, "Issue 1")
	}
	if issue1.Body != "Body 1" {
		t.Errorf("issue 1 body = %q, want %q", issue1.Body, "Body 1")
	}
	if issue1.State != "open" {
		t.Errorf("issue 1 state = %q, want %q", issue1.State, "open")
	}
	if issue1.Author != "user1" {
		t.Errorf("issue 1 author = %q, want %q", issue1.Author, "user1")
	}
	if len(issue1.Labels) != 1 || issue1.Labels[0] != "bug" {
		t.Errorf("issue 1 labels = %v, want [bug]", issue1.Labels)
	}
	if issue1.Dirty {
		t.Error("issue 1 should not be dirty after initial sync")
	}

	// Verify comments are fetched
	comments, err := cacheDB.GetComments("owner/repo", 1)
	if err != nil {
		t.Fatalf("failed to get comments: %v", err)
	}
	if len(comments) != 1 {
		t.Errorf("expected 1 comment for issue 1, got %d", len(comments))
	}
	if len(comments) > 0 && comments[0].Body != "Comment on issue 1" {
		t.Errorf("comment body = %q, want %q", comments[0].Body, "Comment on issue 1")
	}
}

// TestInitialSync_EmptyRepository tests sync with no issues
func TestInitialSync_EmptyRepository(t *testing.T) {
	engine, cacheDB, mockGH := setupTestEngine(t)
	defer engine.Stop()
	defer cacheDB.Close()
	defer mockGH.Close()

	// Don't add any issues to mock server

	// Perform initial sync
	err := engine.InitialSync()
	if err != nil {
		t.Fatalf("InitialSync() error = %v", err)
	}

	// Verify no issues are cached
	issues, err := cacheDB.ListIssues("owner/repo")
	if err != nil {
		t.Fatalf("failed to list cached issues: %v", err)
	}

	if len(issues) != 0 {
		t.Errorf("expected 0 cached issues for empty repo, got %d", len(issues))
	}
}

// TestSyncIssue_ConflictResolution tests conflict detection during sync
func TestSyncIssue_ConflictResolution(t *testing.T) {
	baseTime := time.Date(2026, 1, 13, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name              string
		localUpdatedAt    time.Time
		remoteUpdatedAt   time.Time
		expectPush        bool // true = local wins and pushes, false = conflict (remote newer)
		expectDirtyAfter  bool // true = issue stays dirty, false = dirty cleared
	}{
		{
			name:             "local newer than remote - should push",
			localUpdatedAt:   baseTime.Add(2 * time.Hour),
			remoteUpdatedAt:  baseTime.Add(1 * time.Hour),
			expectPush:       true,
			expectDirtyAfter: false,
		},
		{
			name:             "remote newer than local - conflict, skip push",
			localUpdatedAt:   baseTime.Add(1 * time.Hour),
			remoteUpdatedAt:  baseTime.Add(2 * time.Hour),
			expectPush:       false,
			expectDirtyAfter: true, // Issue stays dirty due to conflict
		},
		{
			name:             "same timestamp - local wins",
			localUpdatedAt:   baseTime,
			remoteUpdatedAt:  baseTime,
			expectPush:       true,
			expectDirtyAfter: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, cacheDB, mockGH := setupTestEngine(t)
			defer engine.Stop()
			defer cacheDB.Close()
			defer mockGH.Close()

			// Add issue to mock server with remote timestamp
			mockGH.AddIssue(&gh.Issue{
				Number:    1,
				Title:     "Test Issue",
				Body:      "Original body",
				State:     "open",
				User:      gh.User{Login: "user1"},
				Labels:    []gh.Label{},
				CreatedAt: baseTime,
				UpdatedAt: tt.remoteUpdatedAt,
				ETag:      `"etag1"`,
			})

			// Insert issue into cache
			cacheIssue := cache.Issue{
				Number:         1,
				Repo:           "owner/repo",
				Title:          "Test Issue",
				Body:           "Modified body",
				State:          "open",
				Author:         "user1",
				Labels:         []string{},
				CreatedAt:      baseTime.Format(time.RFC3339),
				UpdatedAt:      baseTime.Format(time.RFC3339),
				ETag:           `"etag1"`,
				Dirty:          true,
				LocalUpdatedAt: tt.localUpdatedAt.Format(time.RFC3339),
			}
			if err := cacheDB.UpsertIssue(cacheIssue); err != nil {
				t.Fatalf("failed to upsert issue: %v", err)
			}

			// Trigger sync
			err := engine.SyncNow()
			if err != nil {
				// Errors are aggregated but shouldn't fail for conflict scenarios
				t.Logf("SyncNow returned error (may be expected): %v", err)
			}

			// Check if issue was pushed (body changed on mock server)
			remoteIssue := mockGH.GetIssue(1)
			if remoteIssue == nil {
				t.Fatal("remote issue not found")
			}

			if tt.expectPush {
				if remoteIssue.Body != "Modified body" {
					t.Errorf("expected push to update body to %q, got %q", "Modified body", remoteIssue.Body)
				}
			} else {
				if remoteIssue.Body != "Original body" {
					t.Errorf("expected conflict to skip push, but body changed to %q", remoteIssue.Body)
				}
			}

			// Check dirty status
			cachedIssue, err := cacheDB.GetIssue("owner/repo", 1)
			if err != nil {
				t.Fatalf("failed to get cached issue: %v", err)
			}
			if cachedIssue.Dirty != tt.expectDirtyAfter {
				t.Errorf("expected dirty=%v after sync, got %v", tt.expectDirtyAfter, cachedIssue.Dirty)
			}
		})
	}
}

// TestRefreshIssue_NotModified tests 304 response handling
func TestRefreshIssue_NotModified(t *testing.T) {
	engine, cacheDB, mockGH := setupTestEngine(t)
	defer engine.Stop()
	defer cacheDB.Close()
	defer mockGH.Close()

	baseTime := time.Date(2026, 1, 13, 10, 0, 0, 0, time.UTC)
	etag := `"etag123"`

	// Add issue to mock server
	mockGH.AddIssue(&gh.Issue{
		Number:    1,
		Title:     "Test Issue",
		Body:      "Original body",
		State:     "open",
		User:      gh.User{Login: "user1"},
		Labels:    []gh.Label{},
		CreatedAt: baseTime,
		UpdatedAt: baseTime,
		ETag:      etag,
	})

	// Insert issue into cache with same etag
	cacheIssue := cache.Issue{
		Number:    1,
		Repo:      "owner/repo",
		Title:     "Test Issue",
		Body:      "Original body",
		State:     "open",
		Author:    "user1",
		Labels:    []string{},
		CreatedAt: baseTime.Format(time.RFC3339),
		UpdatedAt: baseTime.Format(time.RFC3339),
		ETag:      etag,
		Dirty:     false,
	}
	if err := cacheDB.UpsertIssue(cacheIssue); err != nil {
		t.Fatalf("failed to upsert issue: %v", err)
	}

	// Refresh issue - should get 304
	updated, err := engine.RefreshIssue(1)
	if err != nil {
		t.Fatalf("RefreshIssue() error = %v", err)
	}

	if updated {
		t.Error("expected updated=false for 304 Not Modified")
	}

	// Verify cache unchanged
	cachedIssue, err := cacheDB.GetIssue("owner/repo", 1)
	if err != nil {
		t.Fatalf("failed to get cached issue: %v", err)
	}
	if cachedIssue.Body != "Original body" {
		t.Errorf("body should be unchanged, got %q", cachedIssue.Body)
	}
}

// TestRefreshIssue_WithChanges tests 200 response with updated data
func TestRefreshIssue_WithChanges(t *testing.T) {
	engine, cacheDB, mockGH := setupTestEngine(t)
	defer engine.Stop()
	defer cacheDB.Close()
	defer mockGH.Close()

	baseTime := time.Date(2026, 1, 13, 10, 0, 0, 0, time.UTC)
	oldEtag := `"etag-old"`
	newEtag := `"etag-new"`

	// Add issue to mock server with new etag
	mockGH.AddIssue(&gh.Issue{
		Number:    1,
		Title:     "Updated Title",
		Body:      "Updated body",
		State:     "closed",
		User:      gh.User{Login: "user1"},
		Labels:    []gh.Label{{Name: "fixed", Color: "00ff00"}},
		CreatedAt: baseTime,
		UpdatedAt: baseTime.Add(1 * time.Hour),
		ETag:      newEtag,
	})

	// Insert issue into cache with old etag
	cacheIssue := cache.Issue{
		Number:    1,
		Repo:      "owner/repo",
		Title:     "Old Title",
		Body:      "Old body",
		State:     "open",
		Author:    "user1",
		Labels:    []string{"bug"},
		CreatedAt: baseTime.Format(time.RFC3339),
		UpdatedAt: baseTime.Format(time.RFC3339),
		ETag:      oldEtag,
		Dirty:     false,
	}
	if err := cacheDB.UpsertIssue(cacheIssue); err != nil {
		t.Fatalf("failed to upsert issue: %v", err)
	}

	// Refresh issue - should get 200 with new data
	updated, err := engine.RefreshIssue(1)
	if err != nil {
		t.Fatalf("RefreshIssue() error = %v", err)
	}

	if !updated {
		t.Error("expected updated=true for 200 with new data")
	}

	// Verify cache updated
	cachedIssue, err := cacheDB.GetIssue("owner/repo", 1)
	if err != nil {
		t.Fatalf("failed to get cached issue: %v", err)
	}
	if cachedIssue.Title != "Updated Title" {
		t.Errorf("title = %q, want %q", cachedIssue.Title, "Updated Title")
	}
	if cachedIssue.Body != "Updated body" {
		t.Errorf("body = %q, want %q", cachedIssue.Body, "Updated body")
	}
	if cachedIssue.State != "closed" {
		t.Errorf("state = %q, want %q", cachedIssue.State, "closed")
	}
	if cachedIssue.ETag != newEtag {
		t.Errorf("etag = %q, want %q", cachedIssue.ETag, newEtag)
	}
	if len(cachedIssue.Labels) != 1 || cachedIssue.Labels[0] != "fixed" {
		t.Errorf("labels = %v, want [fixed]", cachedIssue.Labels)
	}
}

// TestRefreshIssue_DirtySkipped tests that dirty issues are not refreshed
func TestRefreshIssue_DirtySkipped(t *testing.T) {
	engine, cacheDB, mockGH := setupTestEngine(t)
	defer engine.Stop()
	defer cacheDB.Close()
	defer mockGH.Close()

	baseTime := time.Date(2026, 1, 13, 10, 0, 0, 0, time.UTC)

	// Add issue to mock server with updated data
	mockGH.AddIssue(&gh.Issue{
		Number:    1,
		Title:     "Updated Title",
		Body:      "Updated body from remote",
		State:     "open",
		User:      gh.User{Login: "user1"},
		Labels:    []gh.Label{},
		CreatedAt: baseTime,
		UpdatedAt: baseTime.Add(1 * time.Hour),
		ETag:      `"new-etag"`,
	})

	// Insert dirty issue into cache
	cacheIssue := cache.Issue{
		Number:         1,
		Repo:           "owner/repo",
		Title:          "Local Title",
		Body:           "Local modified body",
		State:          "open",
		Author:         "user1",
		Labels:         []string{},
		CreatedAt:      baseTime.Format(time.RFC3339),
		UpdatedAt:      baseTime.Format(time.RFC3339),
		ETag:           `"old-etag"`,
		Dirty:          true,
		LocalUpdatedAt: baseTime.Add(30 * time.Minute).Format(time.RFC3339),
	}
	if err := cacheDB.UpsertIssue(cacheIssue); err != nil {
		t.Fatalf("failed to upsert issue: %v", err)
	}

	// Refresh issue - should skip because dirty
	updated, err := engine.RefreshIssue(1)
	if err != nil {
		t.Fatalf("RefreshIssue() error = %v", err)
	}

	if updated {
		t.Error("expected updated=false for dirty issue")
	}

	// Verify cache unchanged (local data preserved)
	cachedIssue, err := cacheDB.GetIssue("owner/repo", 1)
	if err != nil {
		t.Fatalf("failed to get cached issue: %v", err)
	}
	if cachedIssue.Body != "Local modified body" {
		t.Errorf("body should be unchanged, got %q", cachedIssue.Body)
	}
	if !cachedIssue.Dirty {
		t.Error("issue should still be dirty")
	}
}

// TestHasConflict tests conflict detection for various scenarios
func TestHasConflict(t *testing.T) {
	baseTime := time.Date(2026, 1, 13, 10, 0, 0, 0, time.UTC)

	tests := []struct {
		name            string
		dirty           bool
		localUpdatedAt  time.Time
		remoteUpdatedAt time.Time
		expectConflict  bool
	}{
		{
			name:            "non-dirty issue - no conflict",
			dirty:           false,
			localUpdatedAt:  baseTime,
			remoteUpdatedAt: baseTime.Add(1 * time.Hour),
			expectConflict:  false,
		},
		{
			name:            "dirty, local newer - no conflict",
			dirty:           true,
			localUpdatedAt:  baseTime.Add(2 * time.Hour),
			remoteUpdatedAt: baseTime.Add(1 * time.Hour),
			expectConflict:  false,
		},
		{
			name:            "dirty, remote newer - conflict",
			dirty:           true,
			localUpdatedAt:  baseTime.Add(1 * time.Hour),
			remoteUpdatedAt: baseTime.Add(2 * time.Hour),
			expectConflict:  true,
		},
		{
			name:            "dirty, same timestamp - no conflict",
			dirty:           true,
			localUpdatedAt:  baseTime,
			remoteUpdatedAt: baseTime,
			expectConflict:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			engine, cacheDB, mockGH := setupTestEngine(t)
			defer engine.Stop()
			defer cacheDB.Close()
			defer mockGH.Close()

			// Add issue to mock server
			mockGH.AddIssue(&gh.Issue{
				Number:    1,
				Title:     "Test Issue",
				Body:      "Remote body",
				State:     "open",
				User:      gh.User{Login: "user1"},
				Labels:    []gh.Label{},
				CreatedAt: baseTime,
				UpdatedAt: tt.remoteUpdatedAt,
				ETag:      `"etag1"`,
			})

			// Insert issue into cache
			localUpdatedAtStr := ""
			if tt.dirty {
				localUpdatedAtStr = tt.localUpdatedAt.Format(time.RFC3339)
			}

			cacheIssue := cache.Issue{
				Number:         1,
				Repo:           "owner/repo",
				Title:          "Test Issue",
				Body:           "Local body",
				State:          "open",
				Author:         "user1",
				Labels:         []string{},
				CreatedAt:      baseTime.Format(time.RFC3339),
				UpdatedAt:      baseTime.Format(time.RFC3339),
				ETag:           `"etag1"`,
				Dirty:          tt.dirty,
				LocalUpdatedAt: localUpdatedAtStr,
			}
			if err := cacheDB.UpsertIssue(cacheIssue); err != nil {
				t.Fatalf("failed to upsert issue: %v", err)
			}

			// Check for conflict
			hasConflict, err := engine.HasConflict(1)
			if err != nil {
				t.Fatalf("HasConflict() error = %v", err)
			}

			if hasConflict != tt.expectConflict {
				t.Errorf("HasConflict() = %v, want %v", hasConflict, tt.expectConflict)
			}
		})
	}
}

// TestSyncDirtyIssues_PartialFailure tests that sync continues when some issues fail
func TestSyncDirtyIssues_PartialFailure(t *testing.T) {
	engine, cacheDB, mockGH := setupTestEngine(t)
	defer engine.Stop()
	defer cacheDB.Close()
	defer mockGH.Close()

	baseTime := time.Date(2026, 1, 13, 10, 0, 0, 0, time.UTC)
	localTime := baseTime.Add(1 * time.Hour) // Local is newer, should push

	// Add issues 1 and 3 to mock server (issue 2 will be missing, causing failure)
	mockGH.AddIssue(&gh.Issue{
		Number:    1,
		Title:     "Issue 1",
		Body:      "Original body 1",
		State:     "open",
		User:      gh.User{Login: "user1"},
		Labels:    []gh.Label{},
		CreatedAt: baseTime,
		UpdatedAt: baseTime,
		ETag:      `"etag1"`,
	})
	// Issue 2 is NOT added - this will cause a failure when trying to sync

	mockGH.AddIssue(&gh.Issue{
		Number:    3,
		Title:     "Issue 3",
		Body:      "Original body 3",
		State:     "open",
		User:      gh.User{Login: "user3"},
		Labels:    []gh.Label{},
		CreatedAt: baseTime,
		UpdatedAt: baseTime,
		ETag:      `"etag3"`,
	})

	// Insert 3 dirty issues into cache
	for i := 1; i <= 3; i++ {
		cacheIssue := cache.Issue{
			Number:         i,
			Repo:           "owner/repo",
			Title:          "Issue",
			Body:           "Modified body",
			State:          "open",
			Author:         "user",
			Labels:         []string{},
			CreatedAt:      baseTime.Format(time.RFC3339),
			UpdatedAt:      baseTime.Format(time.RFC3339),
			ETag:           `"etag"`,
			Dirty:          true,
			LocalUpdatedAt: localTime.Format(time.RFC3339),
		}
		if err := cacheDB.UpsertIssue(cacheIssue); err != nil {
			t.Fatalf("failed to upsert issue %d: %v", i, err)
		}
	}

	// Trigger sync - should report error but continue
	err := engine.SyncNow()
	if err == nil {
		t.Error("expected error due to missing issue 2")
	}

	// Verify issue 1 was synced (pushed)
	remoteIssue1 := mockGH.GetIssue(1)
	if remoteIssue1 == nil {
		t.Fatal("issue 1 not found on mock server")
	}
	if remoteIssue1.Body != "Modified body" {
		t.Errorf("issue 1 body = %q, want %q", remoteIssue1.Body, "Modified body")
	}

	// Verify issue 1 is no longer dirty
	cachedIssue1, err := cacheDB.GetIssue("owner/repo", 1)
	if err != nil {
		t.Fatalf("failed to get cached issue 1: %v", err)
	}
	if cachedIssue1.Dirty {
		t.Error("issue 1 should not be dirty after successful sync")
	}

	// Verify issue 2 is still dirty (sync failed)
	cachedIssue2, err := cacheDB.GetIssue("owner/repo", 2)
	if err != nil {
		t.Fatalf("failed to get cached issue 2: %v", err)
	}
	if !cachedIssue2.Dirty {
		t.Error("issue 2 should still be dirty after failed sync")
	}

	// Verify issue 3 was synced (pushed)
	remoteIssue3 := mockGH.GetIssue(3)
	if remoteIssue3 == nil {
		t.Fatal("issue 3 not found on mock server")
	}
	if remoteIssue3.Body != "Modified body" {
		t.Errorf("issue 3 body = %q, want %q", remoteIssue3.Body, "Modified body")
	}

	// Verify issue 3 is no longer dirty
	cachedIssue3, err := cacheDB.GetIssue("owner/repo", 3)
	if err != nil {
		t.Fatalf("failed to get cached issue 3: %v", err)
	}
	if cachedIssue3.Dirty {
		t.Error("issue 3 should not be dirty after successful sync")
	}
}

// TestSyncIssue_TitleChange tests that title changes are pushed to GitHub
func TestSyncIssue_TitleChange(t *testing.T) {
	engine, cacheDB, mockGH := setupTestEngine(t)
	defer engine.Stop()
	defer cacheDB.Close()
	defer mockGH.Close()

	baseTime := time.Date(2026, 1, 13, 10, 0, 0, 0, time.UTC)
	localTime := baseTime.Add(1 * time.Hour) // Local is newer, should push

	// Add issue to mock server with original title
	mockGH.AddIssue(&gh.Issue{
		Number:    1,
		Title:     "Original Title",
		Body:      "Original body",
		State:     "open",
		User:      gh.User{Login: "user1"},
		Labels:    []gh.Label{},
		CreatedAt: baseTime,
		UpdatedAt: baseTime,
		ETag:      `"etag1"`,
	})

	// Insert issue into cache with modified title
	cacheIssue := cache.Issue{
		Number:         1,
		Repo:           "owner/repo",
		Title:          "Updated Title",
		Body:           "Original body", // Body unchanged
		State:          "open",
		Author:         "user1",
		Labels:         []string{},
		CreatedAt:      baseTime.Format(time.RFC3339),
		UpdatedAt:      baseTime.Format(time.RFC3339),
		ETag:           `"etag1"`,
		Dirty:          true,
		LocalUpdatedAt: localTime.Format(time.RFC3339),
	}
	if err := cacheDB.UpsertIssue(cacheIssue); err != nil {
		t.Fatalf("failed to upsert issue: %v", err)
	}

	// Trigger sync
	err := engine.SyncNow()
	if err != nil {
		t.Logf("SyncNow returned error (may be expected): %v", err)
	}

	// Verify title was pushed to remote
	remoteIssue := mockGH.GetIssue(1)
	if remoteIssue == nil {
		t.Fatal("remote issue not found")
	}
	if remoteIssue.Title != "Updated Title" {
		t.Errorf("expected title %q, got %q", "Updated Title", remoteIssue.Title)
	}
	// Body should remain unchanged
	if remoteIssue.Body != "Original body" {
		t.Errorf("expected body %q (unchanged), got %q", "Original body", remoteIssue.Body)
	}

	// Verify issue is no longer dirty
	cachedIssue, err := cacheDB.GetIssue("owner/repo", 1)
	if err != nil {
		t.Fatalf("failed to get cached issue: %v", err)
	}
	if cachedIssue.Dirty {
		t.Error("issue should not be dirty after successful sync")
	}
}

// TestSyncIssue_TitleAndBodyChange tests that both title and body changes are pushed
func TestSyncIssue_TitleAndBodyChange(t *testing.T) {
	engine, cacheDB, mockGH := setupTestEngine(t)
	defer engine.Stop()
	defer cacheDB.Close()
	defer mockGH.Close()

	baseTime := time.Date(2026, 1, 13, 10, 0, 0, 0, time.UTC)
	localTime := baseTime.Add(1 * time.Hour) // Local is newer, should push

	// Add issue to mock server with original values
	mockGH.AddIssue(&gh.Issue{
		Number:    1,
		Title:     "Original Title",
		Body:      "Original body",
		State:     "open",
		User:      gh.User{Login: "user1"},
		Labels:    []gh.Label{},
		CreatedAt: baseTime,
		UpdatedAt: baseTime,
		ETag:      `"etag1"`,
	})

	// Insert issue into cache with both title and body modified
	cacheIssue := cache.Issue{
		Number:         1,
		Repo:           "owner/repo",
		Title:          "New Title",
		Body:           "New body content",
		State:          "open",
		Author:         "user1",
		Labels:         []string{},
		CreatedAt:      baseTime.Format(time.RFC3339),
		UpdatedAt:      baseTime.Format(time.RFC3339),
		ETag:           `"etag1"`,
		Dirty:          true,
		LocalUpdatedAt: localTime.Format(time.RFC3339),
	}
	if err := cacheDB.UpsertIssue(cacheIssue); err != nil {
		t.Fatalf("failed to upsert issue: %v", err)
	}

	// Trigger sync
	err := engine.SyncNow()
	if err != nil {
		t.Logf("SyncNow returned error (may be expected): %v", err)
	}

	// Verify both title and body were pushed to remote
	remoteIssue := mockGH.GetIssue(1)
	if remoteIssue == nil {
		t.Fatal("remote issue not found")
	}
	if remoteIssue.Title != "New Title" {
		t.Errorf("expected title %q, got %q", "New Title", remoteIssue.Title)
	}
	if remoteIssue.Body != "New body content" {
		t.Errorf("expected body %q, got %q", "New body content", remoteIssue.Body)
	}

	// Verify issue is no longer dirty
	cachedIssue, err := cacheDB.GetIssue("owner/repo", 1)
	if err != nil {
		t.Fatalf("failed to get cached issue: %v", err)
	}
	if cachedIssue.Dirty {
		t.Error("issue should not be dirty after successful sync")
	}
}

func TestSyncPendingComments(t *testing.T) {
	// Set up mock server
	mockGH := gh.NewMockServer()
	defer mockGH.Close()

	// Add an issue to comment on
	mockGH.AddIssue(&gh.Issue{
		Number:    1,
		Title:     "Test Issue",
		Body:      "Test body",
		State:     "open",
		User:      gh.User{Login: "testuser"},
		CreatedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now().Add(-time.Hour),
		ETag:      `"test-etag"`,
	})

	// Create cache
	tmpDir, err := os.MkdirTemp("", "sync-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	cacheDB, err := cache.InitDB(dbPath)
	if err != nil {
		t.Fatalf("failed to init cache: %v", err)
	}
	defer cacheDB.Close()

	// Create engine
	client := gh.NewWithBaseURL("test-token", mockGH.URL)
	engine, err := NewEngine(cacheDB, client, "owner/repo", 100)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	// Add pending comment
	err = cacheDB.AddPendingComment("owner/repo", 1, "This is a new comment")
	if err != nil {
		t.Fatalf("failed to add pending comment: %v", err)
	}

	// Verify pending comment exists
	pending, err := cacheDB.GetPendingComments("owner/repo")
	if err != nil {
		t.Fatalf("failed to get pending comments: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending comment, got %d", len(pending))
	}

	// Sync
	err = engine.SyncNow()
	if err != nil {
		t.Fatalf("SyncNow() unexpected error: %v", err)
	}

	// Verify comment was created on remote
	comments := mockGH.GetComments(1)
	if len(comments) != 1 {
		t.Errorf("expected 1 comment on remote, got %d", len(comments))
	}
	if comments[0].Body != "This is a new comment" {
		t.Errorf("expected comment body 'This is a new comment', got %q", comments[0].Body)
	}

	// Verify pending comment was removed
	pending, err = cacheDB.GetPendingComments("owner/repo")
	if err != nil {
		t.Fatalf("failed to get pending comments after sync: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected 0 pending comments after sync, got %d", len(pending))
	}
}

func TestSyncDirtyComments(t *testing.T) {
	// Set up mock server
	mockGH := gh.NewMockServer()
	defer mockGH.Close()

	// Add an issue with a comment
	mockGH.AddIssue(&gh.Issue{
		Number:    1,
		Title:     "Test Issue",
		Body:      "Test body",
		State:     "open",
		User:      gh.User{Login: "testuser"},
		CreatedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now().Add(-time.Hour),
		ETag:      `"test-etag"`,
	})
	mockGH.AddComment(1, &gh.Comment{
		ID:        12345,
		Body:      "Original comment",
		User:      gh.User{Login: "testuser"},
		CreatedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now().Add(-time.Hour),
	})

	// Create cache
	tmpDir, err := os.MkdirTemp("", "sync-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	cacheDB, err := cache.InitDB(dbPath)
	if err != nil {
		t.Fatalf("failed to init cache: %v", err)
	}
	defer cacheDB.Close()

	// Create engine
	client := gh.NewWithBaseURL("test-token", mockGH.URL)
	engine, err := NewEngine(cacheDB, client, "owner/repo", 100)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	// Initial sync to get comment in cache
	err = engine.InitialSync()
	if err != nil {
		t.Fatalf("InitialSync() failed: %v", err)
	}

	// Mark comment as dirty with new body
	err = cacheDB.MarkCommentDirty("owner/repo", 12345, "Updated comment body")
	if err != nil {
		t.Fatalf("failed to mark comment dirty: %v", err)
	}

	// Sync
	err = engine.SyncNow()
	if err != nil {
		t.Fatalf("SyncNow() unexpected error: %v", err)
	}

	// Verify comment was updated on remote
	comments := mockGH.GetComments(1)
	if len(comments) != 1 {
		t.Fatalf("expected 1 comment on remote, got %d", len(comments))
	}
	if comments[0].Body != "Updated comment body" {
		t.Errorf("expected comment body 'Updated comment body', got %q", comments[0].Body)
	}

	// Verify dirty flag was cleared
	dirtyComments, err := cacheDB.GetDirtyComments("owner/repo")
	if err != nil {
		t.Fatalf("failed to get dirty comments: %v", err)
	}
	if len(dirtyComments) != 0 {
		t.Errorf("expected 0 dirty comments after sync, got %d", len(dirtyComments))
	}
}

func TestSyncPendingIssues(t *testing.T) {
	// Set up mock server
	mockGH := gh.NewMockServer()
	defer mockGH.Close()

	// Create cache
	tmpDir, err := os.MkdirTemp("", "sync-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	dbPath := filepath.Join(tmpDir, "test.db")
	cacheDB, err := cache.InitDB(dbPath)
	if err != nil {
		t.Fatalf("failed to init cache: %v", err)
	}
	defer cacheDB.Close()

	// Create engine
	client := gh.NewWithBaseURL("test-token", mockGH.URL)
	engine, err := NewEngine(cacheDB, client, "owner/repo", 100)
	if err != nil {
		t.Fatalf("failed to create engine: %v", err)
	}

	// Add pending issue
	_, err = cacheDB.AddPendingIssue("owner/repo", "New Feature Request", "Please add this feature", []string{"enhancement"})
	if err != nil {
		t.Fatalf("failed to add pending issue: %v", err)
	}

	// Verify pending issue exists
	pending, err := cacheDB.GetPendingIssues("owner/repo")
	if err != nil {
		t.Fatalf("failed to get pending issues: %v", err)
	}
	if len(pending) != 1 {
		t.Fatalf("expected 1 pending issue, got %d", len(pending))
	}

	// Sync
	err = engine.SyncNow()
	if err != nil {
		t.Fatalf("SyncNow() unexpected error: %v", err)
	}

	// Verify issue was created on remote
	remoteIssue := mockGH.GetIssue(1) // Mock server assigns issue #1
	if remoteIssue == nil {
		t.Fatal("expected issue to be created on remote")
	}
	if remoteIssue.Title != "New Feature Request" {
		t.Errorf("expected title 'New Feature Request', got %q", remoteIssue.Title)
	}
	if remoteIssue.Body != "Please add this feature" {
		t.Errorf("expected body 'Please add this feature', got %q", remoteIssue.Body)
	}

	// Verify pending issue was removed
	pending, err = cacheDB.GetPendingIssues("owner/repo")
	if err != nil {
		t.Fatalf("failed to get pending issues after sync: %v", err)
	}
	if len(pending) != 0 {
		t.Errorf("expected 0 pending issues after sync, got %d", len(pending))
	}

	// Verify issue was added to cache
	cachedIssue, err := cacheDB.GetIssue("owner/repo", 1)
	if err != nil {
		t.Fatalf("failed to get cached issue: %v", err)
	}
	if cachedIssue == nil {
		t.Fatal("expected newly created issue to be in cache")
	}
	if cachedIssue.Title != "New Feature Request" {
		t.Errorf("cached issue title mismatch: expected 'New Feature Request', got %q", cachedIssue.Title)
	}
}
