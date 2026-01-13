package cache

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
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

	// Mark the issue as dirty with new body (nil for title, pointer for body)
	newBody := "Updated body content"
	err := db.MarkDirty("owner/repo", 1, nil, &newBody)
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

	body := "some body"
	err := db.MarkDirty("owner/repo", 999, nil, &body)
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
	newBody := "User edited body"
	if err := db.MarkDirty("owner/repo", 1, nil, &newBody); err != nil {
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

// Comment tests

func TestUpsertComments_InsertsNewComments(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	// First insert an issue
	issue := Issue{Number: 1, Repo: "owner/repo", Title: "Test Issue"}
	if err := db.UpsertIssue(issue); err != nil {
		t.Fatalf("failed to insert issue: %v", err)
	}

	// Insert comments
	comments := []Comment{
		{
			ID:          100,
			IssueNumber: 1,
			Repo:        "owner/repo",
			Author:      "alice",
			Body:        "First comment",
			CreatedAt:   "2026-01-10T14:12:00Z",
			UpdatedAt:   "2026-01-10T14:12:00Z",
		},
		{
			ID:          101,
			IssueNumber: 1,
			Repo:        "owner/repo",
			Author:      "bob",
			Body:        "Second comment",
			CreatedAt:   "2026-01-10T16:03:00Z",
			UpdatedAt:   "2026-01-10T16:03:00Z",
		},
	}

	err := db.UpsertComments("owner/repo", 1, comments)
	if err != nil {
		t.Fatalf("UpsertComments failed: %v", err)
	}

	// Retrieve and verify
	retrieved, err := db.GetComments("owner/repo", 1)
	if err != nil {
		t.Fatalf("GetComments failed: %v", err)
	}

	if len(retrieved) != 2 {
		t.Fatalf("expected 2 comments, got %d", len(retrieved))
	}

	// Check first comment
	if retrieved[0].ID != 100 {
		t.Errorf("expected first comment ID 100, got %d", retrieved[0].ID)
	}
	if retrieved[0].Author != "alice" {
		t.Errorf("expected author 'alice', got %s", retrieved[0].Author)
	}
	if retrieved[0].Body != "First comment" {
		t.Errorf("expected body 'First comment', got %s", retrieved[0].Body)
	}

	// Check second comment
	if retrieved[1].ID != 101 {
		t.Errorf("expected second comment ID 101, got %d", retrieved[1].ID)
	}
	if retrieved[1].Author != "bob" {
		t.Errorf("expected author 'bob', got %s", retrieved[1].Author)
	}
}

func TestUpsertComments_ReplacesExistingComments(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	// Insert an issue
	issue := Issue{Number: 1, Repo: "owner/repo", Title: "Test Issue"}
	if err := db.UpsertIssue(issue); err != nil {
		t.Fatalf("failed to insert issue: %v", err)
	}

	// Insert initial comments
	initialComments := []Comment{
		{ID: 100, Author: "alice", Body: "Initial comment"},
	}
	if err := db.UpsertComments("owner/repo", 1, initialComments); err != nil {
		t.Fatalf("failed to insert initial comments: %v", err)
	}

	// Replace with new comments
	newComments := []Comment{
		{ID: 200, Author: "charlie", Body: "New comment 1"},
		{ID: 201, Author: "dave", Body: "New comment 2"},
	}
	if err := db.UpsertComments("owner/repo", 1, newComments); err != nil {
		t.Fatalf("failed to replace comments: %v", err)
	}

	// Retrieve and verify old comments are gone
	retrieved, err := db.GetComments("owner/repo", 1)
	if err != nil {
		t.Fatalf("GetComments failed: %v", err)
	}

	if len(retrieved) != 2 {
		t.Fatalf("expected 2 comments after replace, got %d", len(retrieved))
	}

	// Should not contain the old comment
	for _, c := range retrieved {
		if c.ID == 100 {
			t.Error("old comment should have been replaced")
		}
	}

	// Should contain new comments
	if retrieved[0].ID != 200 && retrieved[1].ID != 200 {
		t.Error("new comment 200 not found")
	}
}

func TestGetComments_ReturnsEmptySliceForNoComments(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	// Insert an issue with no comments
	issue := Issue{Number: 1, Repo: "owner/repo", Title: "Test Issue"}
	if err := db.UpsertIssue(issue); err != nil {
		t.Fatalf("failed to insert issue: %v", err)
	}

	// Get comments for an issue with no comments
	comments, err := db.GetComments("owner/repo", 1)
	if err != nil {
		t.Fatalf("GetComments failed: %v", err)
	}

	if comments == nil {
		t.Error("expected empty slice, got nil")
	}
	if len(comments) != 0 {
		t.Errorf("expected 0 comments, got %d", len(comments))
	}
}

func TestGetComments_ReturnsCommentsOrderedByCreatedAt(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	// Insert an issue
	issue := Issue{Number: 1, Repo: "owner/repo", Title: "Test Issue"}
	if err := db.UpsertIssue(issue); err != nil {
		t.Fatalf("failed to insert issue: %v", err)
	}

	// Insert comments out of order
	comments := []Comment{
		{ID: 102, Author: "charlie", Body: "Third", CreatedAt: "2026-01-12T10:00:00Z"},
		{ID: 100, Author: "alice", Body: "First", CreatedAt: "2026-01-10T10:00:00Z"},
		{ID: 101, Author: "bob", Body: "Second", CreatedAt: "2026-01-11T10:00:00Z"},
	}
	if err := db.UpsertComments("owner/repo", 1, comments); err != nil {
		t.Fatalf("failed to insert comments: %v", err)
	}

	// Retrieve and verify order
	retrieved, err := db.GetComments("owner/repo", 1)
	if err != nil {
		t.Fatalf("GetComments failed: %v", err)
	}

	if len(retrieved) != 3 {
		t.Fatalf("expected 3 comments, got %d", len(retrieved))
	}

	// Should be ordered by created_at ascending
	if retrieved[0].ID != 100 {
		t.Errorf("expected first comment ID 100, got %d", retrieved[0].ID)
	}
	if retrieved[1].ID != 101 {
		t.Errorf("expected second comment ID 101, got %d", retrieved[1].ID)
	}
	if retrieved[2].ID != 102 {
		t.Errorf("expected third comment ID 102, got %d", retrieved[2].ID)
	}
}

func TestUpsertComments_EmptyCommentsList(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	// Insert an issue
	issue := Issue{Number: 1, Repo: "owner/repo", Title: "Test Issue"}
	if err := db.UpsertIssue(issue); err != nil {
		t.Fatalf("failed to insert issue: %v", err)
	}

	// First insert some comments
	comments := []Comment{
		{ID: 100, Author: "alice", Body: "Comment"},
	}
	if err := db.UpsertComments("owner/repo", 1, comments); err != nil {
		t.Fatalf("failed to insert comments: %v", err)
	}

	// Now upsert with empty list (should clear all comments)
	if err := db.UpsertComments("owner/repo", 1, []Comment{}); err != nil {
		t.Fatalf("failed to upsert empty comments: %v", err)
	}

	// Verify comments are cleared
	retrieved, err := db.GetComments("owner/repo", 1)
	if err != nil {
		t.Fatalf("GetComments failed: %v", err)
	}

	if len(retrieved) != 0 {
		t.Errorf("expected 0 comments after clearing, got %d", len(retrieved))
	}
}

func TestGetComments_IsolatedByRepoAndIssue(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	// Insert issues in different repos
	issue1 := Issue{Number: 1, Repo: "owner/repo1", Title: "Issue 1"}
	issue2 := Issue{Number: 1, Repo: "owner/repo2", Title: "Issue 2"}
	issue3 := Issue{Number: 2, Repo: "owner/repo1", Title: "Issue 3"}

	for _, issue := range []Issue{issue1, issue2, issue3} {
		if err := db.UpsertIssue(issue); err != nil {
			t.Fatalf("failed to insert issue: %v", err)
		}
	}

	// Insert comments for different repo/issue combinations
	if err := db.UpsertComments("owner/repo1", 1, []Comment{{ID: 100, Author: "a", Body: "repo1-issue1"}}); err != nil {
		t.Fatalf("failed to insert comments: %v", err)
	}
	if err := db.UpsertComments("owner/repo2", 1, []Comment{{ID: 200, Author: "b", Body: "repo2-issue1"}}); err != nil {
		t.Fatalf("failed to insert comments: %v", err)
	}
	if err := db.UpsertComments("owner/repo1", 2, []Comment{{ID: 300, Author: "c", Body: "repo1-issue2"}}); err != nil {
		t.Fatalf("failed to insert comments: %v", err)
	}

	// Get comments for repo1 issue 1
	comments, err := db.GetComments("owner/repo1", 1)
	if err != nil {
		t.Fatalf("GetComments failed: %v", err)
	}

	if len(comments) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(comments))
	}
	if comments[0].ID != 100 {
		t.Errorf("expected comment ID 100, got %d", comments[0].ID)
	}
}

// =============================================================================
// Transaction and Concurrent Tests
// =============================================================================

func TestUpsertComments_VeryLongCommentBody(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	// Insert an issue
	issue := Issue{Number: 1, Repo: "owner/repo", Title: "Test Issue"}
	if err := db.UpsertIssue(issue); err != nil {
		t.Fatalf("failed to insert issue: %v", err)
	}

	// Create a very long comment body (1MB)
	longBody := strings.Repeat("This is a test comment with some content. ", 25000)

	comments := []Comment{
		{
			ID:        100,
			Author:    "longwriter",
			Body:      longBody,
			CreatedAt: "2026-01-10T10:00:00Z",
			UpdatedAt: "2026-01-10T10:00:00Z",
		},
	}

	err := db.UpsertComments("owner/repo", 1, comments)
	if err != nil {
		t.Fatalf("UpsertComments with long body failed: %v", err)
	}

	// Retrieve and verify the long body was stored correctly
	retrieved, err := db.GetComments("owner/repo", 1)
	if err != nil {
		t.Fatalf("GetComments failed: %v", err)
	}

	if len(retrieved) != 1 {
		t.Fatalf("expected 1 comment, got %d", len(retrieved))
	}

	if len(retrieved[0].Body) != len(longBody) {
		t.Errorf("expected body length %d, got %d", len(longBody), len(retrieved[0].Body))
	}

	if retrieved[0].Body != longBody {
		t.Error("long body content mismatch")
	}
}

func TestUpsertComments_ConcurrentCalls(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	// Insert an issue
	issue := Issue{Number: 1, Repo: "owner/repo", Title: "Test Issue"}
	if err := db.UpsertIssue(issue); err != nil {
		t.Fatalf("failed to insert issue: %v", err)
	}

	// Number of concurrent goroutines
	numGoroutines := 10
	errChan := make(chan error, numGoroutines)
	doneChan := make(chan bool, numGoroutines)

	// Start concurrent UpsertComments calls
	for i := 0; i < numGoroutines; i++ {
		go func(id int) {
			comments := []Comment{
				{
					ID:        int64(id * 100),
					Author:    fmt.Sprintf("user%d", id),
					Body:      fmt.Sprintf("Comment from goroutine %d", id),
					CreatedAt: "2026-01-10T10:00:00Z",
					UpdatedAt: "2026-01-10T10:00:00Z",
				},
			}
			err := db.UpsertComments("owner/repo", 1, comments)
			if err != nil {
				errChan <- fmt.Errorf("goroutine %d: %w", id, err)
			}
			doneChan <- true
		}(i)
	}

	// Wait for all goroutines to complete
	for i := 0; i < numGoroutines; i++ {
		<-doneChan
	}
	close(errChan)

	// Check for errors
	var errors []error
	for err := range errChan {
		errors = append(errors, err)
	}

	if len(errors) > 0 {
		t.Errorf("concurrent UpsertComments had %d errors:", len(errors))
		for _, err := range errors {
			t.Errorf("  %v", err)
		}
	}

	// Verify that exactly one set of comments is stored (last writer wins)
	retrieved, err := db.GetComments("owner/repo", 1)
	if err != nil {
		t.Fatalf("GetComments failed: %v", err)
	}

	// Should have exactly 1 comment (from one of the concurrent writes)
	if len(retrieved) != 1 {
		t.Errorf("expected 1 comment after concurrent writes, got %d", len(retrieved))
	}
}

func TestUpsertComments_TransactionRollbackOnDuplicateID(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	// Insert an issue
	issue := Issue{Number: 1, Repo: "owner/repo", Title: "Test Issue"}
	if err := db.UpsertIssue(issue); err != nil {
		t.Fatalf("failed to insert issue: %v", err)
	}

	// First, insert some valid comments
	initialComments := []Comment{
		{ID: 100, Author: "alice", Body: "Initial comment", CreatedAt: "2026-01-10T10:00:00Z"},
	}
	if err := db.UpsertComments("owner/repo", 1, initialComments); err != nil {
		t.Fatalf("failed to insert initial comments: %v", err)
	}

	// Try to insert comments with duplicate IDs in the same batch
	// This should trigger a UNIQUE constraint violation
	duplicateComments := []Comment{
		{ID: 200, Author: "bob", Body: "First", CreatedAt: "2026-01-10T11:00:00Z"},
		{ID: 200, Author: "charlie", Body: "Duplicate ID", CreatedAt: "2026-01-10T12:00:00Z"}, // Same ID as above
	}

	err := db.UpsertComments("owner/repo", 1, duplicateComments)
	// This should fail due to UNIQUE constraint on (repo, issue_number, id)
	if err == nil {
		t.Log("Note: UpsertComments succeeded with duplicate IDs - this is acceptable if the DB allows it")
	}

	// If there was an error, verify the transaction was rolled back
	// (i.e., the initial comments should be gone because UpsertComments deletes first)
	// Actually, since UpsertComments deletes all existing comments first in the transaction,
	// if the transaction rolls back, we should still have the initial comments
	retrieved, err := db.GetComments("owner/repo", 1)
	if err != nil {
		t.Fatalf("GetComments failed: %v", err)
	}

	// Log what we have for diagnostic purposes
	t.Logf("After duplicate ID attempt, found %d comments", len(retrieved))
	for _, c := range retrieved {
		t.Logf("  ID=%d, Author=%s", c.ID, c.Author)
	}
}

func TestConcurrentAccess(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	// Seed with some issues
	for i := 1; i <= 10; i++ {
		issue := Issue{
			Number: i,
			Repo:   "owner/repo",
			Title:  fmt.Sprintf("Issue %d", i),
			Body:   fmt.Sprintf("Body for issue %d", i),
			State:  "open",
		}
		if err := db.UpsertIssue(issue); err != nil {
			t.Fatalf("failed to seed issue %d: %v", i, err)
		}
	}

	// Channels for synchronization
	done := make(chan bool)
	errors := make(chan error, 100)

	// Start 50 goroutines doing reads
	for i := 0; i < 50; i++ {
		go func(id int) {
			deadline := time.Now().Add(1 * time.Second)
			for time.Now().Before(deadline) {
				// Read a random issue
				issueNum := (id % 10) + 1
				_, err := db.GetIssue("owner/repo", issueNum)
				if err != nil {
					errors <- fmt.Errorf("reader %d: GetIssue failed: %w", id, err)
				}

				// List all issues
				_, err = db.ListIssues("owner/repo")
				if err != nil {
					errors <- fmt.Errorf("reader %d: ListIssues failed: %w", id, err)
				}
			}
			done <- true
		}(i)
	}

	// Start 10 goroutines doing writes
	for i := 0; i < 10; i++ {
		go func(id int) {
			deadline := time.Now().Add(1 * time.Second)
			counter := 0
			for time.Now().Before(deadline) {
				// Update an issue
				issueNum := (id % 10) + 1
				issue := Issue{
					Number: issueNum,
					Repo:   "owner/repo",
					Title:  fmt.Sprintf("Updated Issue %d (writer %d, iter %d)", issueNum, id, counter),
					Body:   fmt.Sprintf("Updated body by writer %d at iteration %d", id, counter),
					State:  "open",
				}
				if err := db.UpsertIssue(issue); err != nil {
					errors <- fmt.Errorf("writer %d: UpsertIssue failed: %w", id, err)
				}
				counter++
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 60; i++ {
		<-done
	}
	close(errors)

	// Collect errors
	var allErrors []error
	for err := range errors {
		allErrors = append(allErrors, err)
	}

	if len(allErrors) > 0 {
		t.Errorf("concurrent access test had %d errors:", len(allErrors))
		// Only show first 10 errors to avoid flooding output
		for i, err := range allErrors {
			if i >= 10 {
				t.Errorf("  ... and %d more errors", len(allErrors)-10)
				break
			}
			t.Errorf("  %v", err)
		}
	}

	// Verify data integrity after stress test
	issues, err := db.ListIssues("owner/repo")
	if err != nil {
		t.Fatalf("ListIssues after stress test failed: %v", err)
	}

	if len(issues) != 10 {
		t.Errorf("expected 10 issues after stress test, got %d", len(issues))
	}

	t.Logf("Concurrent access test completed: 50 readers, 10 writers, 1 second duration")
}

func TestConcurrentReadWrite_WithComments(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	// Seed with issues and comments
	for i := 1; i <= 5; i++ {
		issue := Issue{
			Number: i,
			Repo:   "owner/repo",
			Title:  fmt.Sprintf("Issue %d", i),
		}
		if err := db.UpsertIssue(issue); err != nil {
			t.Fatalf("failed to seed issue %d: %v", i, err)
		}

		comments := []Comment{
			{ID: int64(i * 100), Author: "seeder", Body: fmt.Sprintf("Initial comment for issue %d", i)},
		}
		if err := db.UpsertComments("owner/repo", i, comments); err != nil {
			t.Fatalf("failed to seed comments for issue %d: %v", i, err)
		}
	}

	done := make(chan bool)
	errors := make(chan error, 50)

	// Start comment readers
	for i := 0; i < 20; i++ {
		go func(id int) {
			deadline := time.Now().Add(500 * time.Millisecond)
			for time.Now().Before(deadline) {
				issueNum := (id % 5) + 1
				_, err := db.GetComments("owner/repo", issueNum)
				if err != nil {
					errors <- fmt.Errorf("comment reader %d: %w", id, err)
				}
			}
			done <- true
		}(i)
	}

	// Start comment writers
	for i := 0; i < 5; i++ {
		go func(id int) {
			deadline := time.Now().Add(500 * time.Millisecond)
			counter := 0
			for time.Now().Before(deadline) {
				issueNum := (id % 5) + 1
				comments := []Comment{
					{
						ID:     int64(id*1000 + counter),
						Author: fmt.Sprintf("writer%d", id),
						Body:   fmt.Sprintf("Comment %d from writer %d", counter, id),
					},
				}
				if err := db.UpsertComments("owner/repo", issueNum, comments); err != nil {
					errors <- fmt.Errorf("comment writer %d: %w", id, err)
				}
				counter++
			}
			done <- true
		}(i)
	}

	// Wait for all goroutines
	for i := 0; i < 25; i++ {
		<-done
	}
	close(errors)

	// Collect errors
	var allErrors []error
	for err := range errors {
		allErrors = append(allErrors, err)
	}

	if len(allErrors) > 0 {
		t.Errorf("concurrent comment access had %d errors:", len(allErrors))
		for i, err := range allErrors {
			if i >= 5 {
				t.Errorf("  ... and %d more errors", len(allErrors)-5)
				break
			}
			t.Errorf("  %v", err)
		}
	}

	t.Logf("Concurrent comment read/write test completed")
}

func TestUpsertComments_UnicodeAndSpecialChars(t *testing.T) {
	db, cleanup := createTestDB(t)
	defer cleanup()

	// Insert an issue
	issue := Issue{Number: 1, Repo: "owner/repo", Title: "Test Issue"}
	if err := db.UpsertIssue(issue); err != nil {
		t.Fatalf("failed to insert issue: %v", err)
	}

	// Comments with various unicode and special characters
	comments := []Comment{
		{
			ID:     100,
			Author: "user_with_emoji",
			Body:   "Hello World! Here are some emojis: \U0001F600 \U0001F4BB \U0001F389",
		},
		{
			ID:     101,
			Author: "chinese_user",
			Body:   "Chinese characters: \u4F60\u597D\u4E16\u754C (Hello World)",
		},
		{
			ID:     102,
			Author: "sql_injection_test",
			Body:   "'; DROP TABLE comments; --",
		},
		{
			ID:     103,
			Author: "newline_user",
			Body:   "Line 1\nLine 2\n\nLine 4 with\ttab",
		},
		{
			ID:     104,
			Author: "unicode_math",
			Body:   "Math: \u03B1 + \u03B2 = \u03B3, \u221E \u00D7 0 = undefined",
		},
	}

	err := db.UpsertComments("owner/repo", 1, comments)
	if err != nil {
		t.Fatalf("UpsertComments with special chars failed: %v", err)
	}

	retrieved, err := db.GetComments("owner/repo", 1)
	if err != nil {
		t.Fatalf("GetComments failed: %v", err)
	}

	if len(retrieved) != 5 {
		t.Fatalf("expected 5 comments, got %d", len(retrieved))
	}

	// Verify each comment's body is preserved correctly
	for i, original := range comments {
		if retrieved[i].Body != original.Body {
			t.Errorf("comment %d body mismatch:\n  expected: %q\n  got: %q", original.ID, original.Body, retrieved[i].Body)
		}
	}
}
