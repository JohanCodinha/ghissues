package gh

import (
	"bytes"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/JohanCodinha/ghissues/internal/logger"
)

// =============================================================================
// Mock Server Tests (Unit Tests)
// =============================================================================

// TestUpdateIssue_Success tests successful issue update via mock server
func TestUpdateIssue_Success(t *testing.T) {
	mockGH := NewMockServer()
	defer mockGH.Close()

	// Add an issue to update
	mockGH.AddIssue(&Issue{
		Number:    42,
		Title:     "Original Title",
		Body:      "Original body",
		State:     "open",
		User:      User{Login: "testuser"},
		CreatedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now().Add(-time.Hour),
		ETag:      `"original-etag"`,
	})

	client := NewWithBaseURL("test-token", mockGH.URL)

	// Update the issue (only body)
	newBody := "New updated body"
	err := client.UpdateIssue("owner", "repo", 42, IssueUpdate{Body: &newBody})
	if err != nil {
		t.Fatalf("UpdateIssue() unexpected error: %v", err)
	}

	// Verify the issue was updated in the mock server
	updated := mockGH.GetIssue(42)
	if updated.Body != "New updated body" {
		t.Errorf("Expected body 'New updated body', got '%s'", updated.Body)
	}
}

// TestUpdateIssue_NotFound tests 404 response when issue doesn't exist
func TestUpdateIssue_NotFound(t *testing.T) {
	mockGH := NewMockServer()
	defer mockGH.Close()

	client := NewWithBaseURL("test-token", mockGH.URL)

	// Try to update a non-existent issue
	body := "Some body"
	err := client.UpdateIssue("owner", "repo", 999, IssueUpdate{Body: &body})
	if err == nil {
		t.Fatal("UpdateIssue() expected error for non-existent issue, got nil")
	}

	if !strings.Contains(err.Error(), "404") && !strings.Contains(err.Error(), "Not Found") {
		t.Errorf("Expected 404/Not Found error, got: %v", err)
	}
}

// TestUpdateIssue_ValidationError tests 422 response for validation errors
func TestUpdateIssue_ValidationError(t *testing.T) {
	mockGH := NewMockServer()
	defer mockGH.Close()

	// Add an issue first
	mockGH.AddIssue(&Issue{
		Number: 42,
		Title:  "Test Issue",
		Body:   "Test body",
		State:  "open",
		ETag:   `"test-etag"`,
	})

	// Force a 422 validation error
	mockGH.SetNextError(422, `{"message":"Validation Failed","errors":[{"resource":"Issue","field":"body","code":"invalid"}]}`)

	client := NewWithBaseURL("test-token", mockGH.URL)

	body := "Invalid body content"
	err := client.UpdateIssue("owner", "repo", 42, IssueUpdate{Body: &body})
	if err == nil {
		t.Fatal("UpdateIssue() expected validation error, got nil")
	}

	if !strings.Contains(err.Error(), "422") && !strings.Contains(err.Error(), "Validation") {
		t.Errorf("Expected 422/Validation error, got: %v", err)
	}
}

// TestUpdateIssue_RequestBody verifies the request body contains correct JSON
func TestUpdateIssue_RequestBody(t *testing.T) {
	mockGH := NewMockServer()
	defer mockGH.Close()

	// Add an issue
	mockGH.AddIssue(&Issue{
		Number: 1,
		Title:  "Test Issue",
		Body:   "Original body",
		State:  "open",
		ETag:   `"test-etag"`,
	})

	client := NewWithBaseURL("test-token", mockGH.URL)

	newBody := "This is the new body content with special chars: <>&\""
	err := client.UpdateIssue("owner", "repo", 1, IssueUpdate{Body: &newBody})
	if err != nil {
		t.Fatalf("UpdateIssue() unexpected error: %v", err)
	}

	// Verify the body was correctly updated (proves JSON was correctly encoded)
	updated := mockGH.GetIssue(1)
	if updated.Body != newBody {
		t.Errorf("Expected body %q, got %q", newBody, updated.Body)
	}
}

// TestGetIssueWithEtag_NotModified tests 304 Not Modified response
func TestGetIssueWithEtag_NotModified(t *testing.T) {
	mockGH := NewMockServer()
	defer mockGH.Close()

	originalEtag := `"abc123"`
	mockGH.AddIssue(&Issue{
		Number:    42,
		Title:     "Test Issue",
		Body:      "Test body",
		State:     "open",
		User:      User{Login: "testuser"},
		CreatedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now().Add(-time.Hour),
		ETag:      originalEtag,
	})

	client := NewWithBaseURL("test-token", mockGH.URL)

	// Request with matching ETag should return 304
	issue, newEtag, err := client.GetIssueWithEtag("owner", "repo", 42, originalEtag)

	if err != nil {
		t.Fatalf("GetIssueWithEtag() unexpected error: %v", err)
	}

	if issue != nil {
		t.Errorf("Expected nil issue for 304 response, got: %+v", issue)
	}

	if newEtag != "" {
		t.Errorf("Expected empty ETag for 304 response, got: %s", newEtag)
	}
}

// TestGetIssueWithEtag_Modified tests 200 OK with new data
func TestGetIssueWithEtag_Modified(t *testing.T) {
	mockGH := NewMockServer()
	defer mockGH.Close()

	originalEtag := `"abc123"`
	newEtagValue := `"xyz789"`
	mockGH.AddIssue(&Issue{
		Number:    42,
		Title:     "Updated Title",
		Body:      "Updated body",
		State:     "open",
		User:      User{Login: "testuser"},
		CreatedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now(),
		ETag:      newEtagValue,
	})

	client := NewWithBaseURL("test-token", mockGH.URL)

	// Request with different ETag should return full issue
	issue, newEtag, err := client.GetIssueWithEtag("owner", "repo", 42, originalEtag)

	if err != nil {
		t.Fatalf("GetIssueWithEtag() unexpected error: %v", err)
	}

	if issue == nil {
		t.Fatal("Expected issue for 200 response, got nil")
	}

	if issue.Title != "Updated Title" {
		t.Errorf("Expected title 'Updated Title', got '%s'", issue.Title)
	}

	if newEtag != newEtagValue {
		t.Errorf("Expected ETag %s, got %s", newEtagValue, newEtag)
	}

	// Issue's ETag field should also be set
	if issue.ETag != newEtagValue {
		t.Errorf("Expected issue.ETag %s, got %s", newEtagValue, issue.ETag)
	}
}

// TestGetIssueWithEtag_NoEtag tests request without ETag
func TestGetIssueWithEtag_NoEtag(t *testing.T) {
	mockGH := NewMockServer()
	defer mockGH.Close()

	expectedEtag := `"fresh-etag"`
	mockGH.AddIssue(&Issue{
		Number:    42,
		Title:     "Test Issue",
		Body:      "Test body",
		State:     "open",
		User:      User{Login: "testuser"},
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
		ETag:      expectedEtag,
	})

	client := NewWithBaseURL("test-token", mockGH.URL)

	// Request without ETag should always return full issue
	issue, newEtag, err := client.GetIssueWithEtag("owner", "repo", 42, "")

	if err != nil {
		t.Fatalf("GetIssueWithEtag() unexpected error: %v", err)
	}

	if issue == nil {
		t.Fatal("Expected issue, got nil")
	}

	if newEtag != expectedEtag {
		t.Errorf("Expected ETag %s, got %s", expectedEtag, newEtag)
	}
}

// TestListIssues_Pagination tests multi-page issue listing
func TestListIssues_Pagination(t *testing.T) {
	mockGH := NewMockServer()
	defer mockGH.Close()

	// Add 9 issues
	for i := 1; i <= 9; i++ {
		mockGH.AddIssue(&Issue{
			Number:    i,
			Title:     fmt.Sprintf("Issue #%d", i),
			Body:      fmt.Sprintf("Body for issue %d", i),
			State:     "open",
			User:      User{Login: "testuser"},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			ETag:      fmt.Sprintf(`"etag-%d"`, i),
		})
	}

	// Set pagination to 3 issues per page (3 pages total)
	mockGH.SetIssuesPerPage(3)

	client := NewWithBaseURL("test-token", mockGH.URL)

	issues, err := client.ListIssues("owner", "repo")
	if err != nil {
		t.Fatalf("ListIssues() unexpected error: %v", err)
	}

	if len(issues) != 9 {
		t.Errorf("Expected 9 issues, got %d", len(issues))
	}

	// Verify all issues are present
	seen := make(map[int]bool)
	for _, issue := range issues {
		seen[issue.Number] = true
	}
	for i := 1; i <= 9; i++ {
		if !seen[i] {
			t.Errorf("Missing issue #%d in results", i)
		}
	}
}

// TestListIssues_SinglePage tests when all issues fit in one page
func TestListIssues_SinglePage(t *testing.T) {
	mockGH := NewMockServer()
	defer mockGH.Close()

	// Add 3 issues
	for i := 1; i <= 3; i++ {
		mockGH.AddIssue(&Issue{
			Number:    i,
			Title:     fmt.Sprintf("Issue #%d", i),
			Body:      fmt.Sprintf("Body for issue %d", i),
			State:     "open",
			User:      User{Login: "testuser"},
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
			ETag:      fmt.Sprintf(`"etag-%d"`, i),
		})
	}

	// No pagination (default)
	client := NewWithBaseURL("test-token", mockGH.URL)

	issues, err := client.ListIssues("owner", "repo")
	if err != nil {
		t.Fatalf("ListIssues() unexpected error: %v", err)
	}

	if len(issues) != 3 {
		t.Errorf("Expected 3 issues, got %d", len(issues))
	}
}

// TestListComments_Pagination tests multi-page comment listing
func TestListComments_Pagination(t *testing.T) {
	mockGH := NewMockServer()
	defer mockGH.Close()

	// Add an issue first
	mockGH.AddIssue(&Issue{
		Number: 1,
		Title:  "Issue with comments",
		Body:   "Issue body",
		State:  "open",
		ETag:   `"issue-etag"`,
	})

	// Add 7 comments
	for i := 1; i <= 7; i++ {
		mockGH.AddComment(1, &Comment{
			ID:        int64(i),
			User:      User{Login: fmt.Sprintf("user%d", i)},
			Body:      fmt.Sprintf("Comment #%d", i),
			CreatedAt: time.Now(),
			UpdatedAt: time.Now(),
		})
	}

	// Set pagination to 2 comments per page (4 pages total)
	mockGH.SetCommentsPerPage(2)

	client := NewWithBaseURL("test-token", mockGH.URL)

	comments, err := client.ListComments("owner", "repo", 1)
	if err != nil {
		t.Fatalf("ListComments() unexpected error: %v", err)
	}

	if len(comments) != 7 {
		t.Errorf("Expected 7 comments, got %d", len(comments))
	}

	// Verify all comments are present
	seen := make(map[int64]bool)
	for _, comment := range comments {
		seen[comment.ID] = true
	}
	for i := int64(1); i <= 7; i++ {
		if !seen[i] {
			t.Errorf("Missing comment #%d in results", i)
		}
	}
}

// TestListComments_Empty tests listing comments when none exist
func TestListComments_Empty(t *testing.T) {
	mockGH := NewMockServer()
	defer mockGH.Close()

	// Add an issue with no comments
	mockGH.AddIssue(&Issue{
		Number: 1,
		Title:  "Issue without comments",
		Body:   "Issue body",
		State:  "open",
		ETag:   `"issue-etag"`,
	})

	client := NewWithBaseURL("test-token", mockGH.URL)

	comments, err := client.ListComments("owner", "repo", 1)
	if err != nil {
		t.Fatalf("ListComments() unexpected error: %v", err)
	}

	if len(comments) != 0 {
		t.Errorf("Expected 0 comments, got %d", len(comments))
	}
}

// TestGetIssue_WithMock tests GetIssue with mock server
func TestGetIssue_WithMock(t *testing.T) {
	mockGH := NewMockServer()
	defer mockGH.Close()

	expectedEtag := `"test-etag-123"`
	mockGH.AddIssue(&Issue{
		Number:    42,
		Title:     "Test Issue Title",
		Body:      "Test issue body content",
		State:     "open",
		User:      User{Login: "author"},
		Labels:    []Label{{Name: "bug", Color: "ff0000"}},
		CreatedAt: time.Date(2024, 1, 15, 10, 0, 0, 0, time.UTC),
		UpdatedAt: time.Date(2024, 1, 16, 12, 0, 0, 0, time.UTC),
		ETag:      expectedEtag,
	})

	client := NewWithBaseURL("test-token", mockGH.URL)

	issue, etag, err := client.GetIssue("owner", "repo", 42)
	if err != nil {
		t.Fatalf("GetIssue() unexpected error: %v", err)
	}

	if issue == nil {
		t.Fatal("Expected issue, got nil")
	}

	if issue.Number != 42 {
		t.Errorf("Expected issue number 42, got %d", issue.Number)
	}

	if issue.Title != "Test Issue Title" {
		t.Errorf("Expected title 'Test Issue Title', got '%s'", issue.Title)
	}

	if etag != expectedEtag {
		t.Errorf("Expected ETag %s, got %s", expectedEtag, etag)
	}
}

// TestGetIssue_NotFound tests 404 response
func TestGetIssue_NotFound(t *testing.T) {
	mockGH := NewMockServer()
	defer mockGH.Close()

	client := NewWithBaseURL("test-token", mockGH.URL)

	issue, etag, err := client.GetIssue("owner", "repo", 9999)

	if err == nil {
		t.Fatal("Expected error for non-existent issue, got nil")
	}

	if issue != nil {
		t.Errorf("Expected nil issue, got %+v", issue)
	}

	if etag != "" {
		t.Errorf("Expected empty ETag, got %s", etag)
	}

	if !strings.Contains(err.Error(), "404") && !strings.Contains(err.Error(), "not found") {
		t.Errorf("Expected 404/not found error, got: %v", err)
	}
}

// TestListIssues_Empty tests listing when no issues exist
func TestListIssues_Empty(t *testing.T) {
	mockGH := NewMockServer()
	defer mockGH.Close()

	client := NewWithBaseURL("test-token", mockGH.URL)

	issues, err := client.ListIssues("owner", "repo")
	if err != nil {
		t.Fatalf("ListIssues() unexpected error: %v", err)
	}

	if len(issues) != 0 {
		t.Errorf("Expected 0 issues, got %d", len(issues))
	}
}

// =============================================================================
// Integration Tests (require real GitHub token)
// =============================================================================

func TestGetToken(t *testing.T) {
	token, err := GetToken()
	if err != nil {
		t.Skipf("Skipping: no GitHub token available (%v)", err)
	}

	if token == "" {
		t.Fatal("GetToken() returned empty token")
	}

	// Token should start with gho_ (GitHub OAuth) or ghp_ (GitHub PAT)
	if !strings.HasPrefix(token, "gho_") && !strings.HasPrefix(token, "ghp_") {
		t.Logf("Token has unexpected prefix (first 4 chars): %s", token[:4])
	}

	t.Logf("Token retrieved successfully (prefix: %s...)", token[:10])
}

func TestListIssues_Integration(t *testing.T) {
	token, err := GetToken()
	if err != nil {
		t.Skipf("Skipping: no GitHub token available (%v)", err)
	}

	client := New(token)
	issues, err := client.ListIssues("JohanCodinha", "ghissues")
	if err != nil {
		t.Fatalf("ListIssues() failed: %v", err)
	}

	if len(issues) == 0 {
		t.Fatal("ListIssues() returned no issues, expected at least 1")
	}

	t.Logf("Found %d issues:", len(issues))
	for _, issue := range issues {
		t.Logf("  #%d: %s (state: %s, author: %s)", issue.Number, issue.Title, issue.State, issue.User.Login)
	}
}

func TestGetIssue_Integration(t *testing.T) {
	token, err := GetToken()
	if err != nil {
		t.Skipf("Skipping: no GitHub token available (%v)", err)
	}

	client := New(token)
	issue, etag, err := client.GetIssue("JohanCodinha", "ghissues", 1)
	if err != nil {
		t.Fatalf("GetIssue() failed: %v", err)
	}

	if issue == nil {
		t.Fatal("GetIssue() returned nil issue")
	}

	if issue.Number != 1 {
		t.Errorf("Expected issue number 1, got %d", issue.Number)
	}

	if etag == "" {
		t.Error("GetIssue() returned empty ETag")
	}

	t.Logf("Issue #%d:", issue.Number)
	t.Logf("  Title: %s", issue.Title)
	t.Logf("  State: %s", issue.State)
	t.Logf("  Author: %s", issue.User.Login)
	t.Logf("  Created: %s", issue.CreatedAt.Format("2006-01-02 15:04:05"))
	t.Logf("  Updated: %s", issue.UpdatedAt.Format("2006-01-02 15:04:05"))
	t.Logf("  ETag: %s", etag)
	t.Logf("  Body preview: %s...", truncate(issue.Body, 100))
}

func TestListComments_Integration(t *testing.T) {
	token, err := GetToken()
	if err != nil {
		t.Skipf("Skipping: no GitHub token available (%v)", err)
	}

	client := New(token)
	comments, err := client.ListComments("JohanCodinha", "ghissues", 1)
	if err != nil {
		t.Fatalf("ListComments() failed: %v", err)
	}

	t.Logf("Found %d comments for issue #1:", len(comments))
	for _, comment := range comments {
		t.Logf("  Comment #%d by %s at %s", comment.ID, comment.User.Login, comment.CreatedAt.Format("2006-01-02 15:04:05"))
		t.Logf("    Body preview: %s...", truncate(comment.Body, 50))
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}

// TestMain can be used to run tests manually with verbose output
func TestMain(m *testing.M) {
	fmt.Println("Running GitHub API client tests...")
	fmt.Println("Note: Integration tests require valid GitHub authentication.")
	fmt.Println()
	os.Exit(m.Run())
}

// =============================================================================
// getTokenFromGhConfig Tests
// =============================================================================

func TestGetTokenFromGhConfigPath_ValidConfig(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, ".config", "gh")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	hostsYml := `github.com:
    oauth_token: test-token-12345
    user: testuser
`
	configPath := filepath.Join(configDir, "hosts.yml")
	if err := os.WriteFile(configPath, []byte(hostsYml), 0644); err != nil {
		t.Fatalf("failed to write hosts.yml: %v", err)
	}

	token, err := getTokenFromGhConfigPath(configPath)
	if err != nil {
		t.Fatalf("getTokenFromGhConfigPath() unexpected error: %v", err)
	}

	if token != "test-token-12345" {
		t.Errorf("expected token 'test-token-12345', got '%s'", token)
	}
}

func TestGetTokenFromGhConfigPath_MissingOAuthToken(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, ".config", "gh")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	// Config without oauth_token field
	hostsYml := `github.com:
    user: testuser
`
	configPath := filepath.Join(configDir, "hosts.yml")
	if err := os.WriteFile(configPath, []byte(hostsYml), 0644); err != nil {
		t.Fatalf("failed to write hosts.yml: %v", err)
	}

	token, err := getTokenFromGhConfigPath(configPath)
	if err == nil {
		t.Fatal("expected error for missing oauth_token, got nil")
	}

	if token != "" {
		t.Errorf("expected empty token, got '%s'", token)
	}

	if !strings.Contains(err.Error(), "no oauth_token found") {
		t.Errorf("expected 'no oauth_token found' error, got: %v", err)
	}
}

func TestGetTokenFromGhConfigPath_EmptyOAuthToken(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, ".config", "gh")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	// Config with empty oauth_token
	hostsYml := `github.com:
    oauth_token: ""
    user: testuser
`
	configPath := filepath.Join(configDir, "hosts.yml")
	if err := os.WriteFile(configPath, []byte(hostsYml), 0644); err != nil {
		t.Fatalf("failed to write hosts.yml: %v", err)
	}

	token, err := getTokenFromGhConfigPath(configPath)
	if err == nil {
		t.Fatal("expected error for empty oauth_token, got nil")
	}

	if token != "" {
		t.Errorf("expected empty token, got '%s'", token)
	}
}

func TestGetTokenFromGhConfigPath_MalformedYAML(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, ".config", "gh")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	// Invalid YAML
	hostsYml := `github.com:
    oauth_token: [invalid yaml
    not proper: indentation
`
	configPath := filepath.Join(configDir, "hosts.yml")
	if err := os.WriteFile(configPath, []byte(hostsYml), 0644); err != nil {
		t.Fatalf("failed to write hosts.yml: %v", err)
	}

	token, err := getTokenFromGhConfigPath(configPath)
	if err == nil {
		t.Fatal("expected error for malformed YAML, got nil")
	}

	if token != "" {
		t.Errorf("expected empty token, got '%s'", token)
	}

	if !strings.Contains(err.Error(), "failed to parse") {
		t.Errorf("expected 'failed to parse' error, got: %v", err)
	}
}

func TestGetTokenFromGhConfigPath_MissingFile(t *testing.T) {
	tmpDir := t.TempDir()
	configPath := filepath.Join(tmpDir, "nonexistent", "hosts.yml")

	token, err := getTokenFromGhConfigPath(configPath)
	if err == nil {
		t.Fatal("expected error for missing file, got nil")
	}

	if token != "" {
		t.Errorf("expected empty token, got '%s'", token)
	}

	if !strings.Contains(err.Error(), "failed to read") {
		t.Errorf("expected 'failed to read' error, got: %v", err)
	}
}

func TestGetTokenFromGhConfigPath_NoGitHubHost(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, ".config", "gh")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	// Config with different host, not github.com
	hostsYml := `gitlab.com:
    oauth_token: gitlab-token
    user: testuser
`
	configPath := filepath.Join(configDir, "hosts.yml")
	if err := os.WriteFile(configPath, []byte(hostsYml), 0644); err != nil {
		t.Fatalf("failed to write hosts.yml: %v", err)
	}

	token, err := getTokenFromGhConfigPath(configPath)
	if err == nil {
		t.Fatal("expected error for missing github.com host, got nil")
	}

	if token != "" {
		t.Errorf("expected empty token, got '%s'", token)
	}
}

func TestGetTokenFromGhConfigPath_MultipleHosts(t *testing.T) {
	tmpDir := t.TempDir()
	configDir := filepath.Join(tmpDir, ".config", "gh")
	if err := os.MkdirAll(configDir, 0755); err != nil {
		t.Fatalf("failed to create config dir: %v", err)
	}

	// Config with multiple hosts
	hostsYml := `github.enterprise.com:
    oauth_token: enterprise-token
    user: enterpriseuser
github.com:
    oauth_token: public-token-xyz
    user: publicuser
`
	configPath := filepath.Join(configDir, "hosts.yml")
	if err := os.WriteFile(configPath, []byte(hostsYml), 0644); err != nil {
		t.Fatalf("failed to write hosts.yml: %v", err)
	}

	token, err := getTokenFromGhConfigPath(configPath)
	if err != nil {
		t.Fatalf("getTokenFromGhConfigPath() unexpected error: %v", err)
	}

	// Should return the github.com token, not the enterprise one
	if token != "public-token-xyz" {
		t.Errorf("expected token 'public-token-xyz', got '%s'", token)
	}
}

// =============================================================================
// checkRateLimit Tests
// =============================================================================

func TestCheckRateLimit_LogsWarning(t *testing.T) {
	// Create a response with X-RateLimit-Remaining: 0
	resp := &http.Response{
		Header: make(http.Header),
	}
	resp.Header.Set("X-RateLimit-Remaining", "0")
	// Set reset time to 1 hour from now
	resetTime := time.Now().Add(1 * time.Hour).Unix()
	resp.Header.Set("X-RateLimit-Reset", fmt.Sprintf("%d", resetTime))

	// Capture logger output
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	logger.SetLevel(logger.LevelWarn)
	defer func() {
		logger.SetOutput(os.Stderr)
		logger.SetLevel(logger.LevelInfo)
	}()

	// Call checkRateLimit
	checkRateLimit(resp)

	output := buf.String()

	// Verify warning was logged
	if !strings.Contains(output, "WARN") {
		t.Errorf("expected warning to be logged, got: %s", output)
	}
	if !strings.Contains(output, "rate limit exceeded") {
		t.Errorf("expected 'rate limit exceeded' in output, got: %s", output)
	}
}

func TestCheckRateLimit_NoWarningWhenRemainingPositive(t *testing.T) {
	// Create a response with remaining requests
	resp := &http.Response{
		Header: make(http.Header),
	}
	resp.Header.Set("X-RateLimit-Remaining", "100")
	resp.Header.Set("X-RateLimit-Reset", fmt.Sprintf("%d", time.Now().Add(1*time.Hour).Unix()))

	// Capture logger output
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	logger.SetLevel(logger.LevelWarn)
	defer func() {
		logger.SetOutput(os.Stderr)
		logger.SetLevel(logger.LevelInfo)
	}()

	// Call checkRateLimit
	checkRateLimit(resp)

	output := buf.String()

	// Verify no warning was logged
	if output != "" {
		t.Errorf("expected no output when rate limit remaining > 0, got: %s", output)
	}
}

func TestCheckRateLimit_NoWarningWhenHeaderMissing(t *testing.T) {
	// Create a response without rate limit headers
	resp := &http.Response{
		Header: make(http.Header),
	}

	// Capture logger output
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	logger.SetLevel(logger.LevelWarn)
	defer func() {
		logger.SetOutput(os.Stderr)
		logger.SetLevel(logger.LevelInfo)
	}()

	// Call checkRateLimit
	checkRateLimit(resp)

	output := buf.String()

	// Verify no warning was logged
	if output != "" {
		t.Errorf("expected no output when headers missing, got: %s", output)
	}
}

func TestCheckRateLimit_NoWarningWhenResetMissing(t *testing.T) {
	// Create a response with remaining=0 but no reset header
	resp := &http.Response{
		Header: make(http.Header),
	}
	resp.Header.Set("X-RateLimit-Remaining", "0")
	// No X-RateLimit-Reset header

	// Capture logger output
	var buf bytes.Buffer
	logger.SetOutput(&buf)
	logger.SetLevel(logger.LevelWarn)
	defer func() {
		logger.SetOutput(os.Stderr)
		logger.SetLevel(logger.LevelInfo)
	}()

	// Call checkRateLimit
	checkRateLimit(resp)

	output := buf.String()

	// No warning should be logged because reset header is missing
	if output != "" {
		t.Errorf("expected no output when reset header missing, got: %s", output)
	}
}

// =============================================================================
// CreateComment Tests
// =============================================================================

func TestCreateComment_Success(t *testing.T) {
	mockGH := NewMockServer()
	defer mockGH.Close()

	// Add an issue to comment on
	mockGH.AddIssue(&Issue{
		Number: 42,
		Title:  "Test Issue",
		Body:   "Test body",
		State:  "open",
		ETag:   `"test-etag"`,
	})

	client := NewWithBaseURL("test-token", mockGH.URL)

	comment, err := client.CreateComment("owner", "repo", 42, "This is a new comment")
	if err != nil {
		t.Fatalf("CreateComment() unexpected error: %v", err)
	}

	if comment.ID == 0 {
		t.Error("Expected non-zero comment ID")
	}
	if comment.Body != "This is a new comment" {
		t.Errorf("Expected body 'This is a new comment', got '%s'", comment.Body)
	}

	// Verify comment was added to mock server
	comments := mockGH.GetComments(42)
	if len(comments) != 1 {
		t.Errorf("Expected 1 comment in mock server, got %d", len(comments))
	}
}

func TestCreateComment_IssueNotFound(t *testing.T) {
	mockGH := NewMockServer()
	defer mockGH.Close()

	client := NewWithBaseURL("test-token", mockGH.URL)

	_, err := client.CreateComment("owner", "repo", 999, "Comment on non-existent issue")
	if err == nil {
		t.Fatal("CreateComment() expected error for non-existent issue, got nil")
	}

	if !strings.Contains(err.Error(), "404") && !strings.Contains(err.Error(), "Not Found") {
		t.Errorf("Expected 404/Not Found error, got: %v", err)
	}
}

// =============================================================================
// UpdateComment Tests
// =============================================================================

func TestUpdateComment_Success(t *testing.T) {
	mockGH := NewMockServer()
	defer mockGH.Close()

	// Add an issue and a comment
	mockGH.AddIssue(&Issue{
		Number: 42,
		Title:  "Test Issue",
		State:  "open",
	})
	mockGH.AddComment(42, &Comment{
		ID:        12345,
		Body:      "Original comment body",
		User:      User{Login: "testuser"},
		CreatedAt: time.Now().Add(-time.Hour),
		UpdatedAt: time.Now().Add(-time.Hour),
	})

	client := NewWithBaseURL("test-token", mockGH.URL)

	err := client.UpdateComment("owner", "repo", 12345, "Updated comment body")
	if err != nil {
		t.Fatalf("UpdateComment() unexpected error: %v", err)
	}

	// Verify comment was updated in mock server
	comments := mockGH.GetComments(42)
	if len(comments) != 1 {
		t.Fatalf("Expected 1 comment, got %d", len(comments))
	}
	if comments[0].Body != "Updated comment body" {
		t.Errorf("Expected body 'Updated comment body', got '%s'", comments[0].Body)
	}
}

func TestUpdateComment_NotFound(t *testing.T) {
	mockGH := NewMockServer()
	defer mockGH.Close()

	client := NewWithBaseURL("test-token", mockGH.URL)

	err := client.UpdateComment("owner", "repo", 99999, "Update non-existent comment")
	if err == nil {
		t.Fatal("UpdateComment() expected error for non-existent comment, got nil")
	}

	if !strings.Contains(err.Error(), "404") && !strings.Contains(err.Error(), "Not Found") {
		t.Errorf("Expected 404/Not Found error, got: %v", err)
	}
}

// =============================================================================
// CreateIssue Tests
// =============================================================================

func TestCreateIssue_Success(t *testing.T) {
	mockGH := NewMockServer()
	defer mockGH.Close()

	client := NewWithBaseURL("test-token", mockGH.URL)

	issue, err := client.CreateIssue("owner", "repo", "New Issue Title", "Issue body content", []string{"bug", "p1"})
	if err != nil {
		t.Fatalf("CreateIssue() unexpected error: %v", err)
	}

	if issue.Number == 0 {
		t.Error("Expected non-zero issue number")
	}
	if issue.Title != "New Issue Title" {
		t.Errorf("Expected title 'New Issue Title', got '%s'", issue.Title)
	}
	if issue.Body != "Issue body content" {
		t.Errorf("Expected body 'Issue body content', got '%s'", issue.Body)
	}
	if issue.State != "open" {
		t.Errorf("Expected state 'open', got '%s'", issue.State)
	}
	if len(issue.Labels) != 2 {
		t.Errorf("Expected 2 labels, got %d", len(issue.Labels))
	}
	if issue.ETag == "" {
		t.Error("Expected non-empty ETag")
	}

	// Verify issue was added to mock server
	created := mockGH.GetIssue(issue.Number)
	if created == nil {
		t.Fatal("Issue not found in mock server")
	}
	if created.Title != "New Issue Title" {
		t.Errorf("Mock server issue title mismatch: expected 'New Issue Title', got '%s'", created.Title)
	}
}

func TestCreateIssue_NoLabels(t *testing.T) {
	mockGH := NewMockServer()
	defer mockGH.Close()

	client := NewWithBaseURL("test-token", mockGH.URL)

	issue, err := client.CreateIssue("owner", "repo", "Issue Without Labels", "Body", nil)
	if err != nil {
		t.Fatalf("CreateIssue() unexpected error: %v", err)
	}

	if issue.Number == 0 {
		t.Error("Expected non-zero issue number")
	}
	if len(issue.Labels) != 0 {
		t.Errorf("Expected 0 labels, got %d", len(issue.Labels))
	}
}

func TestCreateIssue_Error(t *testing.T) {
	mockGH := NewMockServer()
	defer mockGH.Close()

	// Force a validation error
	mockGH.SetNextError(422, `{"message":"Validation Failed","errors":[{"resource":"Issue","field":"title","code":"missing"}]}`)

	client := NewWithBaseURL("test-token", mockGH.URL)

	_, err := client.CreateIssue("owner", "repo", "", "Body without title", nil)
	if err == nil {
		t.Fatal("CreateIssue() expected validation error, got nil")
	}

	if !strings.Contains(err.Error(), "422") && !strings.Contains(err.Error(), "Validation") {
		t.Errorf("Expected 422/Validation error, got: %v", err)
	}
}
