package gh

import (
	"fmt"
	"os"
	"strings"
	"testing"
)

func TestGetToken(t *testing.T) {
	token, err := GetToken()
	if err != nil {
		t.Fatalf("GetToken() failed: %v", err)
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

func TestListIssues(t *testing.T) {
	token, err := GetToken()
	if err != nil {
		t.Fatalf("GetToken() failed: %v", err)
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

func TestGetIssue(t *testing.T) {
	token, err := GetToken()
	if err != nil {
		t.Fatalf("GetToken() failed: %v", err)
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

func TestUpdateIssueCompiles(t *testing.T) {
	// This test verifies UpdateIssue compiles and can be called
	// We don't actually call it to avoid modifying the test issue

	token, err := GetToken()
	if err != nil {
		t.Fatalf("GetToken() failed: %v", err)
	}

	client := New(token)

	// Just verify the method exists and has the right signature
	var _ func(string, string, int, string) error = client.UpdateIssue

	t.Log("UpdateIssue() compiles correctly")
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
	fmt.Println("Note: These tests require valid GitHub authentication.")
	fmt.Println()
	os.Exit(m.Run())
}
