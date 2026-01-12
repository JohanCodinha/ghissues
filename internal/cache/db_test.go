package cache

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// createTestDB creates a temporary database for testing and returns the DB and a cleanup function.
func createTestDB(t *testing.T) (*DB, func()) {
	t.Helper()

	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("failed to create test database: %v", err)
	}

	cleanup := func() {
		db.Close()
	}

	return db, cleanup
}

func TestInitDB_CreatesTable(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	db, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("InitDB failed: %v", err)
	}
	defer db.Close()

	// Verify the database file was created
	if _, err := os.Stat(dbPath); os.IsNotExist(err) {
		t.Error("database file was not created")
	}

	// Verify the table exists by querying it
	var tableName string
	err = db.conn.QueryRow("SELECT name FROM sqlite_master WHERE type='table' AND name='issues'").Scan(&tableName)
	if err != nil {
		t.Errorf("failed to find issues table: %v", err)
	}
	if tableName != "issues" {
		t.Errorf("expected table name 'issues', got %s", tableName)
	}
}

func TestInitDB_CanReopenExistingDB(t *testing.T) {
	tmpDir := t.TempDir()
	dbPath := filepath.Join(tmpDir, "test.db")

	// Create and close the database
	db1, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("first InitDB failed: %v", err)
	}

	// Insert a test issue
	issue := Issue{
		Number: 1,
		Repo:   "owner/repo",
		Title:  "Test Issue",
	}
	if err := db1.UpsertIssue(issue); err != nil {
		t.Fatalf("failed to insert issue: %v", err)
	}
	db1.Close()

	// Reopen the database
	db2, err := InitDB(dbPath)
	if err != nil {
		t.Fatalf("second InitDB failed: %v", err)
	}
	defer db2.Close()

	// Verify the issue is still there
	retrieved, err := db2.GetIssue("owner/repo", 1)
	if err != nil {
		t.Fatalf("failed to get issue: %v", err)
	}
	if retrieved == nil {
		t.Error("issue not found after reopening database")
	}
	if retrieved.Title != "Test Issue" {
		t.Errorf("expected title 'Test Issue', got %s", retrieved.Title)
	}
}

func TestUpsertIssue_InsertsNewIssue(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	issue := Issue{
		Number:    42,
		Repo:      "owner/repo",
		Title:     "Test Issue",
		Body:      "This is the body",
		State:     "open",
		Author:    "testuser",
		Labels:    []string{"bug", "enhancement"},
		CreatedAt: "2024-01-01T00:00:00Z",
		UpdatedAt: "2024-01-02T00:00:00Z",
		ETag:      `"abc123"`,
		Dirty:     false,
	}

	err := db.UpsertIssue(issue)
	if err != nil {
		t.Fatalf("UpsertIssue failed: %v", err)
	}

	// Verify the issue was inserted
	retrieved, err := db.GetIssue("owner/repo", 42)
	if err != nil {
		t.Fatalf("GetIssue failed: %v", err)
	}
	if retrieved == nil {
		t.Fatal("issue not found after insert")
	}

	// Check all fields
	if retrieved.Number != 42 {
		t.Errorf("expected number 42, got %d", retrieved.Number)
	}
	if retrieved.Repo != "owner/repo" {
		t.Errorf("expected repo 'owner/repo', got %s", retrieved.Repo)
	}
	if retrieved.Title != "Test Issue" {
		t.Errorf("expected title 'Test Issue', got %s", retrieved.Title)
	}
	if retrieved.Body != "This is the body" {
		t.Errorf("expected body 'This is the body', got %s", retrieved.Body)
	}
	if retrieved.State != "open" {
		t.Errorf("expected state 'open', got %s", retrieved.State)
	}
	if retrieved.Author != "testuser" {
		t.Errorf("expected author 'testuser', got %s", retrieved.Author)
	}
	if len(retrieved.Labels) != 2 || retrieved.Labels[0] != "bug" || retrieved.Labels[1] != "enhancement" {
		t.Errorf("expected labels [bug, enhancement], got %v", retrieved.Labels)
	}
	if retrieved.CreatedAt != "2024-01-01T00:00:00Z" {
		t.Errorf("expected createdAt '2024-01-01T00:00:00Z', got %s", retrieved.CreatedAt)
	}
	if retrieved.UpdatedAt != "2024-01-02T00:00:00Z" {
		t.Errorf("expected updatedAt '2024-01-02T00:00:00Z', got %s", retrieved.UpdatedAt)
	}
	if retrieved.ETag != `"abc123"` {
		t.Errorf("expected etag '\"abc123\"', got %s", retrieved.ETag)
	}
	if retrieved.Dirty {
		t.Error("expected dirty to be false")
	}
}

func TestUpsertIssue_UpdatesExistingIssue(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	// Insert initial issue
	issue := Issue{
		Number: 1,
		Repo:   "owner/repo",
		Title:  "Original Title",
		Body:   "Original Body",
		State:  "open",
	}
	if err := db.UpsertIssue(issue); err != nil {
		t.Fatalf("initial insert failed: %v", err)
	}

	// Update the issue (same repo + number)
	issue.Title = "Updated Title"
	issue.Body = "Updated Body"
	issue.State = "closed"
	issue.Labels = []string{"wontfix"}

	if err := db.UpsertIssue(issue); err != nil {
		t.Fatalf("update failed: %v", err)
	}

	// Verify only one issue exists and it has updated values
	issues, err := db.ListIssues("owner/repo")
	if err != nil {
		t.Fatalf("ListIssues failed: %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("expected 1 issue, got %d", len(issues))
	}

	retrieved := issues[0]
	if retrieved.Title != "Updated Title" {
		t.Errorf("expected updated title 'Updated Title', got %s", retrieved.Title)
	}
	if retrieved.Body != "Updated Body" {
		t.Errorf("expected updated body 'Updated Body', got %s", retrieved.Body)
	}
	if retrieved.State != "closed" {
		t.Errorf("expected updated state 'closed', got %s", retrieved.State)
	}
	if len(retrieved.Labels) != 1 || retrieved.Labels[0] != "wontfix" {
		t.Errorf("expected labels [wontfix], got %v", retrieved.Labels)
	}
}

func TestUpsertIssue_HandlesNullFields(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	// Insert issue with minimal fields (optional fields empty)
	issue := Issue{
		Number: 1,
		Repo:   "owner/repo",
		Title:  "Minimal Issue",
	}

	if err := db.UpsertIssue(issue); err != nil {
		t.Fatalf("UpsertIssue failed: %v", err)
	}

	retrieved, err := db.GetIssue("owner/repo", 1)
	if err != nil {
		t.Fatalf("GetIssue failed: %v", err)
	}

	if retrieved.Body != "" {
		t.Errorf("expected empty body, got %s", retrieved.Body)
	}
	if retrieved.State != "" {
		t.Errorf("expected empty state, got %s", retrieved.State)
	}
	if retrieved.Labels != nil && len(retrieved.Labels) != 0 {
		t.Errorf("expected empty labels, got %v", retrieved.Labels)
	}
}

func TestGetIssue_ReturnsNilForNonExistent(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	issue, err := db.GetIssue("owner/repo", 999)
	if err != nil {
		t.Fatalf("GetIssue returned error: %v", err)
	}
	if issue != nil {
		t.Error("expected nil for non-existent issue")
	}
}

func TestListIssues_ReturnsAllIssuesForRepo(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	// Insert issues for two different repos
	issues := []Issue{
		{Number: 1, Repo: "owner/repo1", Title: "Issue 1"},
		{Number: 2, Repo: "owner/repo1", Title: "Issue 2"},
		{Number: 3, Repo: "owner/repo1", Title: "Issue 3"},
		{Number: 1, Repo: "owner/repo2", Title: "Other Repo Issue"},
	}

	for _, issue := range issues {
		if err := db.UpsertIssue(issue); err != nil {
			t.Fatalf("failed to insert issue: %v", err)
		}
	}

	// List issues for repo1
	repo1Issues, err := db.ListIssues("owner/repo1")
	if err != nil {
		t.Fatalf("ListIssues failed: %v", err)
	}

	if len(repo1Issues) != 3 {
		t.Fatalf("expected 3 issues for repo1, got %d", len(repo1Issues))
	}

	// Verify they are ordered by number
	if repo1Issues[0].Number != 1 || repo1Issues[1].Number != 2 || repo1Issues[2].Number != 3 {
		t.Error("issues not ordered by number")
	}

	// List issues for repo2
	repo2Issues, err := db.ListIssues("owner/repo2")
	if err != nil {
		t.Fatalf("ListIssues for repo2 failed: %v", err)
	}

	if len(repo2Issues) != 1 {
		t.Fatalf("expected 1 issue for repo2, got %d", len(repo2Issues))
	}
}

func TestListIssues_ReturnsEmptySliceForNoIssues(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	issues, err := db.ListIssues("nonexistent/repo")
	if err != nil {
		t.Fatalf("ListIssues failed: %v", err)
	}

	if issues == nil {
		t.Error("expected empty slice, got nil")
	}
	if len(issues) != 0 {
		t.Errorf("expected 0 issues, got %d", len(issues))
	}
}

func TestMarkDirty_UpdatesBodyAndSetsFlag(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	// Insert initial issue
	issue := Issue{
		Number: 1,
		Repo:   "owner/repo",
		Title:  "Test Issue",
		Body:   "Original body",
		Dirty:  false,
	}
	if err := db.UpsertIssue(issue); err != nil {
		t.Fatalf("failed to insert issue: %v", err)
	}

	// Record time before marking dirty (with 1 second buffer for timing issues)
	beforeMark := time.Now().UTC().Add(-1 * time.Second)

	// Mark the issue as dirty with new body
	err := db.MarkDirty("owner/repo", 1, "Updated body content")
	if err != nil {
		t.Fatalf("MarkDirty failed: %v", err)
	}

	// Record time after marking dirty (with 1 second buffer for timing issues)
	afterMark := time.Now().UTC().Add(1 * time.Second)

	// Retrieve and verify
	retrieved, err := db.GetIssue("owner/repo", 1)
	if err != nil {
		t.Fatalf("GetIssue failed: %v", err)
	}

	if retrieved.Body != "Updated body content" {
		t.Errorf("expected body 'Updated body content', got %s", retrieved.Body)
	}
	if !retrieved.Dirty {
		t.Error("expected dirty flag to be true")
	}
	if retrieved.LocalUpdatedAt == "" {
		t.Error("expected local_updated_at to be set")
	}

	// Verify the timestamp is in RFC3339 format and within expected range
	localTime, err := time.Parse(time.RFC3339, retrieved.LocalUpdatedAt)
	if err != nil {
		t.Errorf("local_updated_at is not in RFC3339 format: %v", err)
	}
	if localTime.Before(beforeMark) || localTime.After(afterMark) {
		t.Errorf("local_updated_at (%s) not within expected range [%s, %s]", retrieved.LocalUpdatedAt, beforeMark.Format(time.RFC3339), afterMark.Format(time.RFC3339))
	}
}

func TestMarkDirty_ReturnsErrorForNonExistentIssue(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	err := db.MarkDirty("owner/repo", 999, "some body")
	if err == nil {
		t.Error("expected error for non-existent issue")
	}
}

func TestGetDirtyIssues_ReturnsOnlyDirtyIssues(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	// Insert mix of dirty and non-dirty issues
	issues := []Issue{
		{Number: 1, Repo: "owner/repo", Title: "Clean Issue 1", Dirty: false},
		{Number: 2, Repo: "owner/repo", Title: "Dirty Issue 2", Dirty: true},
		{Number: 3, Repo: "owner/repo", Title: "Clean Issue 3", Dirty: false},
		{Number: 4, Repo: "owner/repo", Title: "Dirty Issue 4", Dirty: true},
		{Number: 5, Repo: "other/repo", Title: "Dirty Other Repo", Dirty: true},
	}

	for _, issue := range issues {
		if err := db.UpsertIssue(issue); err != nil {
			t.Fatalf("failed to insert issue: %v", err)
		}
	}

	// Get dirty issues for owner/repo
	dirtyIssues, err := db.GetDirtyIssues("owner/repo")
	if err != nil {
		t.Fatalf("GetDirtyIssues failed: %v", err)
	}

	if len(dirtyIssues) != 2 {
		t.Fatalf("expected 2 dirty issues, got %d", len(dirtyIssues))
	}

	// Verify both are dirty
	for _, issue := range dirtyIssues {
		if !issue.Dirty {
			t.Errorf("issue %d should be dirty", issue.Number)
		}
	}

	// Verify numbers are correct and ordered
	if dirtyIssues[0].Number != 2 || dirtyIssues[1].Number != 4 {
		t.Error("wrong dirty issues returned")
	}
}

func TestGetDirtyIssues_ReturnsEmptySliceWhenNoDirtyIssues(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	// Insert only clean issues
	if err := db.UpsertIssue(Issue{Number: 1, Repo: "owner/repo", Title: "Clean", Dirty: false}); err != nil {
		t.Fatalf("failed to insert issue: %v", err)
	}

	dirtyIssues, err := db.GetDirtyIssues("owner/repo")
	if err != nil {
		t.Fatalf("GetDirtyIssues failed: %v", err)
	}

	if len(dirtyIssues) != 0 {
		t.Errorf("expected 0 dirty issues, got %d", len(dirtyIssues))
	}
}

func TestClearDirty_ResetsDirtyFlag(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	// Insert a dirty issue
	issue := Issue{
		Number: 1,
		Repo:   "owner/repo",
		Title:  "Dirty Issue",
		Dirty:  true,
	}
	if err := db.UpsertIssue(issue); err != nil {
		t.Fatalf("failed to insert issue: %v", err)
	}

	// Verify it's dirty
	retrieved, _ := db.GetIssue("owner/repo", 1)
	if !retrieved.Dirty {
		t.Fatal("issue should be dirty before ClearDirty")
	}

	// Clear the dirty flag
	err := db.ClearDirty("owner/repo", 1)
	if err != nil {
		t.Fatalf("ClearDirty failed: %v", err)
	}

	// Verify it's now clean
	retrieved, _ = db.GetIssue("owner/repo", 1)
	if retrieved.Dirty {
		t.Error("expected dirty flag to be false after ClearDirty")
	}
}

func TestClearDirty_ReturnsErrorForNonExistentIssue(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	err := db.ClearDirty("owner/repo", 999)
	if err == nil {
		t.Error("expected error for non-existent issue")
	}
}

func TestWorkflow_MarkDirtyThenClear(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	// Simulate the workflow: insert, mark dirty, sync (clear dirty)
	issue := Issue{
		Number: 1,
		Repo:   "owner/repo",
		Title:  "Test Issue",
		Body:   "Original body",
	}
	if err := db.UpsertIssue(issue); err != nil {
		t.Fatalf("failed to insert: %v", err)
	}

	// User edits the file locally
	if err := db.MarkDirty("owner/repo", 1, "User edited body"); err != nil {
		t.Fatalf("failed to mark dirty: %v", err)
	}

	// Get dirty issues for sync
	dirtyIssues, err := db.GetDirtyIssues("owner/repo")
	if err != nil {
		t.Fatalf("failed to get dirty issues: %v", err)
	}
	if len(dirtyIssues) != 1 {
		t.Fatalf("expected 1 dirty issue, got %d", len(dirtyIssues))
	}
	if dirtyIssues[0].Body != "User edited body" {
		t.Error("dirty issue has wrong body")
	}

	// After successful sync to GitHub, clear dirty flag
	if err := db.ClearDirty("owner/repo", 1); err != nil {
		t.Fatalf("failed to clear dirty: %v", err)
	}

	// Verify no more dirty issues
	dirtyIssues, err = db.GetDirtyIssues("owner/repo")
	if err != nil {
		t.Fatalf("failed to get dirty issues after clear: %v", err)
	}
	if len(dirtyIssues) != 0 {
		t.Error("expected no dirty issues after clear")
	}
}

func TestLabelsJSONSerialization(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	// Test various label scenarios
	testCases := []struct {
		name   string
		labels []string
	}{
		{"empty labels", nil},
		{"empty slice", []string{}},
		{"single label", []string{"bug"}},
		{"multiple labels", []string{"bug", "enhancement", "help wanted"}},
		{"labels with special chars", []string{"good first issue", "type: bug", "priority/high"}},
	}

	for i, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			issue := Issue{
				Number: i + 1,
				Repo:   "owner/repo",
				Title:  tc.name,
				Labels: tc.labels,
			}
			if err := db.UpsertIssue(issue); err != nil {
				t.Fatalf("failed to insert: %v", err)
			}

			retrieved, err := db.GetIssue("owner/repo", i+1)
			if err != nil {
				t.Fatalf("failed to get: %v", err)
			}

			// Handle nil vs empty slice comparison
			if tc.labels == nil {
				if retrieved.Labels != nil && len(retrieved.Labels) > 0 {
					t.Errorf("expected nil/empty labels, got %v", retrieved.Labels)
				}
			} else if len(tc.labels) == 0 {
				if len(retrieved.Labels) != 0 {
					t.Errorf("expected empty labels, got %v", retrieved.Labels)
				}
			} else {
				if len(retrieved.Labels) != len(tc.labels) {
					t.Errorf("expected %d labels, got %d", len(tc.labels), len(retrieved.Labels))
				}
				for j, label := range tc.labels {
					if retrieved.Labels[j] != label {
						t.Errorf("label mismatch at index %d: expected %s, got %s", j, label, retrieved.Labels[j])
					}
				}
			}
		})
	}
}

func TestClose_CanCloseMultipleTimes(t *testing.T) {
	db, _ := createTestDB(t)

	// First close should succeed
	if err := db.Close(); err != nil {
		t.Errorf("first close failed: %v", err)
	}

	// Second close should also not panic (but will return error)
	// This tests that Close handles the closed state gracefully
	_ = db.Close()
}

func TestUniqueConstraint_SameRepoAndNumber(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	// Insert first issue
	issue1 := Issue{
		Number: 1,
		Repo:   "owner/repo",
		Title:  "First",
	}
	if err := db.UpsertIssue(issue1); err != nil {
		t.Fatalf("first insert failed: %v", err)
	}

	// Insert same repo+number should replace
	issue2 := Issue{
		Number: 1,
		Repo:   "owner/repo",
		Title:  "Second",
	}
	if err := db.UpsertIssue(issue2); err != nil {
		t.Fatalf("second insert failed: %v", err)
	}

	// Should only have one issue
	issues, _ := db.ListIssues("owner/repo")
	if len(issues) != 1 {
		t.Errorf("expected 1 issue, got %d", len(issues))
	}
	if issues[0].Title != "Second" {
		t.Errorf("expected title 'Second', got %s", issues[0].Title)
	}
}

func TestSameNumberDifferentRepos(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	// Insert issue #1 in two different repos
	issue1 := Issue{Number: 1, Repo: "owner/repo1", Title: "Repo 1 Issue"}
	issue2 := Issue{Number: 1, Repo: "owner/repo2", Title: "Repo 2 Issue"}

	if err := db.UpsertIssue(issue1); err != nil {
		t.Fatalf("failed to insert issue1: %v", err)
	}
	if err := db.UpsertIssue(issue2); err != nil {
		t.Fatalf("failed to insert issue2: %v", err)
	}

	// Both should exist
	retrieved1, _ := db.GetIssue("owner/repo1", 1)
	retrieved2, _ := db.GetIssue("owner/repo2", 1)

	if retrieved1 == nil || retrieved1.Title != "Repo 1 Issue" {
		t.Error("repo1 issue not found or wrong title")
	}
	if retrieved2 == nil || retrieved2.Title != "Repo 2 Issue" {
		t.Error("repo2 issue not found or wrong title")
	}
}
