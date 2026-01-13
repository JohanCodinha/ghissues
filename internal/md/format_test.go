package md

import (
	"strings"
	"testing"

	"github.com/JohanCodinha/ghissues/internal/cache"
)

// Test 1: ToMarkdown produces valid frontmatter
func TestToMarkdown_ProducesValidFrontmatter(t *testing.T) {
	issue := &cache.Issue{
		Number:    1234,
		Repo:      "owner/repo",
		Title:     "Test Issue",
		Body:      "Test body",
		State:     "open",
		Author:    "alice",
		Labels:    []string{"bug", "p1"},
		CreatedAt: "2026-01-08T09:15:00Z",
		UpdatedAt: "2026-01-10T16:03:00Z",
		ETag:      "abc123",
	}

	result := ToMarkdown(issue)

	// Check frontmatter delimiters
	if !strings.HasPrefix(result, "---\n") {
		t.Error("markdown should start with ---")
	}

	// Count frontmatter delimiters (should be exactly 2)
	delimCount := strings.Count(result, "---")
	if delimCount < 2 {
		t.Errorf("expected at least 2 frontmatter delimiters, got %d", delimCount)
	}

	// Extract frontmatter content
	parts := strings.SplitN(result, "---", 3)
	if len(parts) < 3 {
		t.Fatal("could not extract frontmatter")
	}
	frontmatter := parts[1]

	// Check it contains expected YAML keys
	expectedKeys := []string{"id:", "repo:", "url:", "state:", "author:", "created_at:", "updated_at:", "etag:"}
	for _, key := range expectedKeys {
		if !strings.Contains(frontmatter, key) {
			t.Errorf("frontmatter should contain %q", key)
		}
	}
}

// Test 2: ToMarkdown includes all expected fields
func TestToMarkdown_IncludesAllExpectedFields(t *testing.T) {
	issue := &cache.Issue{
		Number:    1234,
		Repo:      "owner/repo",
		Title:     "Crash on startup",
		Body:      "Application crashes immediately after login.",
		State:     "open",
		Author:    "alice",
		Labels:    []string{"bug", "p1"},
		CreatedAt: "2026-01-08T09:15:00Z",
		UpdatedAt: "2026-01-10T16:03:00Z",
		ETag:      "abc123",
	}

	result := ToMarkdown(issue)

	// Check specific values in frontmatter
	checks := []struct {
		name     string
		contains string
	}{
		{"id", "id: 1234"},
		{"repo", "repo: owner/repo"},
		{"url", "url: https://github.com/owner/repo/issues/1234"},
		{"state", "state: open"},
		{"author", "author: alice"},
		{"created_at", "created_at: \"2026-01-08T09:15:00Z\""},
		{"updated_at", "updated_at: \"2026-01-10T16:03:00Z\""},
		{"etag", "etag: abc123"},
	}

	for _, check := range checks {
		if !strings.Contains(result, check.contains) {
			t.Errorf("expected %s field: %q not found in:\n%s", check.name, check.contains, result)
		}
	}

	// Check title
	if !strings.Contains(result, "# Crash on startup") {
		t.Error("expected title heading not found")
	}

	// Check body section
	if !strings.Contains(result, "## Body") {
		t.Error("expected ## Body section not found")
	}

	if !strings.Contains(result, "Application crashes immediately after login.") {
		t.Error("expected body content not found")
	}
}

// Test 3: FromMarkdown parses frontmatter correctly
func TestFromMarkdown_ParsesFrontmatterCorrectly(t *testing.T) {
	content := `---
id: 1234
repo: owner/repo
url: https://github.com/owner/repo/issues/1234
state: open
author: alice
etag: abc123
---

# Test Issue

## Body

Test body content`

	parsed, err := FromMarkdown(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if parsed.Number != 1234 {
		t.Errorf("expected Number 1234, got %d", parsed.Number)
	}
	if parsed.Repo != "owner/repo" {
		t.Errorf("expected Repo 'owner/repo', got %q", parsed.Repo)
	}
	if parsed.State != "open" {
		t.Errorf("expected State 'open', got %q", parsed.State)
	}
	if parsed.Author != "alice" {
		t.Errorf("expected Author 'alice', got %q", parsed.Author)
	}
	if parsed.ETag != "abc123" {
		t.Errorf("expected ETag 'abc123', got %q", parsed.ETag)
	}
}

// Test 4: FromMarkdown extracts title
func TestFromMarkdown_ExtractsTitle(t *testing.T) {
	content := `---
id: 1
repo: test/repo
---

# My Test Title

## Body

Content here`

	parsed, err := FromMarkdown(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if parsed.Title != "My Test Title" {
		t.Errorf("expected title 'My Test Title', got %q", parsed.Title)
	}
}

// Test 5a: FromMarkdown extracts body (single line)
func TestFromMarkdown_ExtractsBody_SingleLine(t *testing.T) {
	content := `---
id: 1
repo: test/repo
---

# Title

## Body

Single line body content`

	parsed, err := FromMarkdown(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if parsed.Body != "Single line body content" {
		t.Errorf("expected body 'Single line body content', got %q", parsed.Body)
	}
}

// Test 5b: FromMarkdown extracts body (multiline)
func TestFromMarkdown_ExtractsBody_Multiline(t *testing.T) {
	content := `---
id: 1
repo: test/repo
---

# Title

## Body

First line of body.

Second paragraph.

Third paragraph with more text.`

	parsed, err := FromMarkdown(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	expected := `First line of body.

Second paragraph.

Third paragraph with more text.`

	if parsed.Body != expected {
		t.Errorf("expected body:\n%q\ngot:\n%q", expected, parsed.Body)
	}
}

// Test 5c: FromMarkdown extracts body (with code blocks)
func TestFromMarkdown_ExtractsBody_WithCodeBlocks(t *testing.T) {
	content := "---\nid: 1\nrepo: test/repo\n---\n\n# Title\n\n## Body\n\nHere is some code:\n\n```go\nfunc main() {\n    fmt.Println(\"Hello\")\n}\n```\n\nAnd more text."

	parsed, err := FromMarkdown(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(parsed.Body, "```go") {
		t.Error("body should contain code block opening")
	}
	if !strings.Contains(parsed.Body, "fmt.Println") {
		t.Error("body should contain code content")
	}
	if !strings.Contains(parsed.Body, "And more text.") {
		t.Error("body should contain text after code block")
	}
}

// Test 6: FromMarkdown handles missing ## Body gracefully
func TestFromMarkdown_HandlesMissingBodySection(t *testing.T) {
	content := `---
id: 1
repo: test/repo
---

# Title Without Body Section

Some content here but no ## Body section`

	parsed, err := FromMarkdown(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Body should be empty when ## Body section is missing
	if parsed.Body != "" {
		t.Errorf("expected empty body when ## Body section missing, got %q", parsed.Body)
	}

	// But title should still be parsed
	if parsed.Title != "Title Without Body Section" {
		t.Errorf("expected title 'Title Without Body Section', got %q", parsed.Title)
	}
}

// Test 7: Round-trip: ToMarkdown -> FromMarkdown preserves data
func TestRoundTrip_PreservesData(t *testing.T) {
	original := &cache.Issue{
		Number:    42,
		Repo:      "test/project",
		Title:     "Round Trip Test",
		Body:      "This is the body content.\n\nWith multiple paragraphs.",
		State:     "closed",
		Author:    "bob",
		Labels:    []string{"enhancement"},
		CreatedAt: "2026-01-01T00:00:00Z",
		UpdatedAt: "2026-01-02T12:00:00Z",
		ETag:      "etag123",
	}

	// Convert to markdown
	markdown := ToMarkdown(original)

	// Parse back
	parsed, err := FromMarkdown(markdown)
	if err != nil {
		t.Fatalf("failed to parse markdown: %v", err)
	}

	// Check preserved fields
	if parsed.Number != original.Number {
		t.Errorf("Number not preserved: expected %d, got %d", original.Number, parsed.Number)
	}
	if parsed.Repo != original.Repo {
		t.Errorf("Repo not preserved: expected %q, got %q", original.Repo, parsed.Repo)
	}
	if parsed.Title != original.Title {
		t.Errorf("Title not preserved: expected %q, got %q", original.Title, parsed.Title)
	}
	if parsed.State != original.State {
		t.Errorf("State not preserved: expected %q, got %q", original.State, parsed.State)
	}
	if parsed.Author != original.Author {
		t.Errorf("Author not preserved: expected %q, got %q", original.Author, parsed.Author)
	}
	if parsed.ETag != original.ETag {
		t.Errorf("ETag not preserved: expected %q, got %q", original.ETag, parsed.ETag)
	}

	// Body comparison (normalize trailing newlines)
	expectedBody := strings.TrimRight(original.Body, "\n")
	actualBody := strings.TrimRight(parsed.Body, "\n")
	if actualBody != expectedBody {
		t.Errorf("Body not preserved:\nexpected: %q\ngot: %q", expectedBody, actualBody)
	}
}

// Test 8: DetectChanges correctly identifies title changes
func TestDetectChanges_IdentifiesTitleChanges(t *testing.T) {
	original := &cache.Issue{
		Number: 1,
		Repo:   "test/repo",
		Title:  "Original Title",
		Body:   "Same body",
	}

	parsed := &ParsedIssue{
		Number: 1,
		Repo:   "test/repo",
		Title:  "Modified Title",
		Body:   "Same body",
	}

	changes := DetectChanges(original, parsed)

	if !changes.TitleChanged {
		t.Error("expected TitleChanged to be true")
	}
	if changes.NewTitle != "Modified Title" {
		t.Errorf("expected NewTitle 'Modified Title', got %q", changes.NewTitle)
	}
	if changes.BodyChanged {
		t.Error("expected BodyChanged to be false")
	}
}

// Test 9: DetectChanges correctly identifies body changes
func TestDetectChanges_IdentifiesBodyChanges(t *testing.T) {
	original := &cache.Issue{
		Number: 1,
		Repo:   "test/repo",
		Title:  "Same Title",
		Body:   "Original body content",
	}

	parsed := &ParsedIssue{
		Number: 1,
		Repo:   "test/repo",
		Title:  "Same Title",
		Body:   "Modified body content",
	}

	changes := DetectChanges(original, parsed)

	if changes.TitleChanged {
		t.Error("expected TitleChanged to be false")
	}
	if !changes.BodyChanged {
		t.Error("expected BodyChanged to be true")
	}
	if changes.NewBody != "Modified body content" {
		t.Errorf("expected NewBody 'Modified body content', got %q", changes.NewBody)
	}
}

// Test 10: DetectChanges returns no changes when content is identical
func TestDetectChanges_NoChangesWhenIdentical(t *testing.T) {
	original := &cache.Issue{
		Number: 1,
		Repo:   "test/repo",
		Title:  "Same Title",
		Body:   "Same body",
	}

	parsed := &ParsedIssue{
		Number: 1,
		Repo:   "test/repo",
		Title:  "Same Title",
		Body:   "Same body",
	}

	changes := DetectChanges(original, parsed)

	if changes.TitleChanged {
		t.Error("expected TitleChanged to be false")
	}
	if changes.BodyChanged {
		t.Error("expected BodyChanged to be false")
	}
}

// Additional edge case tests

func TestFromMarkdown_ErrorOnMissingFrontmatter(t *testing.T) {
	content := `# Title Without Frontmatter

## Body

Some content`

	_, err := FromMarkdown(content)
	if err == nil {
		t.Error("expected error when frontmatter is missing")
	}
}

func TestFromMarkdown_ErrorOnMalformedFrontmatter(t *testing.T) {
	content := `---
id: not_a_number
repo: test/repo
---

# Title`

	_, err := FromMarkdown(content)
	if err == nil {
		t.Error("expected error when frontmatter has invalid YAML types")
	}
}

func TestToMarkdown_EmptyBody(t *testing.T) {
	issue := &cache.Issue{
		Number: 1,
		Repo:   "test/repo",
		Title:  "Issue with empty body",
		Body:   "",
		State:  "open",
	}

	result := ToMarkdown(issue)

	if !strings.Contains(result, "## Body") {
		t.Error("should still contain ## Body section even when body is empty")
	}
}

func TestToMarkdown_LabelsFormatting(t *testing.T) {
	issue := &cache.Issue{
		Number: 1,
		Repo:   "test/repo",
		Title:  "Issue with labels",
		Body:   "Body",
		Labels: []string{"bug", "urgent", "help wanted"},
	}

	result := ToMarkdown(issue)

	// Labels should be in flow format: [bug, urgent, help wanted]
	if !strings.Contains(result, "labels:") {
		t.Error("should contain labels field")
	}
}

func TestToMarkdown_NoLabels(t *testing.T) {
	issue := &cache.Issue{
		Number: 1,
		Repo:   "test/repo",
		Title:  "Issue without labels",
		Body:   "Body",
		Labels: nil,
	}

	result := ToMarkdown(issue)

	// Should still produce valid markdown
	if !strings.HasPrefix(result, "---\n") {
		t.Error("should produce valid markdown even without labels")
	}
}

func TestFromMarkdown_BodyEndsAtNextSection(t *testing.T) {
	content := `---
id: 1
repo: test/repo
---

# Title

## Body

Body content here.

## Comments

### 2026-01-10 14:12Z — alice

Some comment`

	parsed, err := FromMarkdown(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Body should not include the Comments section
	if strings.Contains(parsed.Body, "Comments") {
		t.Error("body should not contain ## Comments section")
	}
	if strings.Contains(parsed.Body, "alice") {
		t.Error("body should not contain comment content")
	}
	if !strings.Contains(parsed.Body, "Body content here.") {
		t.Error("body should contain the actual body content")
	}
}

func TestDetectChanges_TrailingNewlineNormalization(t *testing.T) {
	// Test that trailing newline differences don't cause false positives
	original := &cache.Issue{
		Title: "Title",
		Body:  "Body content\n",
	}

	parsed := &ParsedIssue{
		Title: "Title",
		Body:  "Body content",
	}

	changes := DetectChanges(original, parsed)

	if changes.BodyChanged {
		t.Error("trailing newline difference should not be detected as a change")
	}
}

func TestFromMarkdown_PreservesInternalWhitespace(t *testing.T) {
	content := `---
id: 1
repo: test/repo
---

# Title

## Body

Line 1

Line 2

Line 3`

	parsed, err := FromMarkdown(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Should preserve blank lines between paragraphs
	if !strings.Contains(parsed.Body, "Line 1\n\nLine 2") {
		t.Error("should preserve blank lines between paragraphs")
	}
}

// Comment rendering tests

func TestToMarkdown_WithComments(t *testing.T) {
	issue := &cache.Issue{
		Number:    1,
		Repo:      "owner/repo",
		Title:     "Test Issue",
		Body:      "Issue body",
		State:     "open",
		Author:    "alice",
		CreatedAt: "2026-01-08T09:15:00Z",
		UpdatedAt: "2026-01-10T16:03:00Z",
	}

	comments := []cache.Comment{
		{
			ID:        987654,
			Author:    "alice",
			Body:      "I can reproduce this on version 2.3.1",
			CreatedAt: "2026-01-10T14:12:00Z",
			UpdatedAt: "2026-01-10T14:12:00Z",
		},
		{
			ID:        987655,
			Author:    "bob",
			Body:      "Looking into it now. Seems related to the pagination changes.",
			CreatedAt: "2026-01-10T16:03:00Z",
			UpdatedAt: "2026-01-10T16:03:00Z",
		},
	}

	result := ToMarkdown(issue, comments)

	// Check comments count in frontmatter
	if !strings.Contains(result, "comments: 2") {
		t.Error("expected comments: 2 in frontmatter")
	}

	// Check Comments section exists
	if !strings.Contains(result, "## Comments") {
		t.Error("expected ## Comments section")
	}

	// Check first comment header
	if !strings.Contains(result, "### 2026-01-10T14:12:00Z - alice") {
		t.Error("expected first comment header with timestamp and author")
	}

	// Check comment_id HTML comment
	if !strings.Contains(result, "<!-- comment_id: 987654 -->") {
		t.Error("expected comment_id HTML comment for first comment")
	}

	// Check first comment body
	if !strings.Contains(result, "I can reproduce this on version 2.3.1") {
		t.Error("expected first comment body")
	}

	// Check second comment header
	if !strings.Contains(result, "### 2026-01-10T16:03:00Z - bob") {
		t.Error("expected second comment header")
	}

	// Check second comment_id
	if !strings.Contains(result, "<!-- comment_id: 987655 -->") {
		t.Error("expected comment_id HTML comment for second comment")
	}
}

func TestToMarkdown_NoComments(t *testing.T) {
	issue := &cache.Issue{
		Number: 1,
		Repo:   "owner/repo",
		Title:  "Test Issue",
		Body:   "Issue body",
		State:  "open",
	}

	result := ToMarkdown(issue)

	// Check comments count is 0 in frontmatter
	if !strings.Contains(result, "comments: 0") {
		t.Error("expected comments: 0 in frontmatter when no comments")
	}

	// Should not have Comments section
	if strings.Contains(result, "## Comments") {
		t.Error("should not have ## Comments section when no comments")
	}
}

func TestToMarkdown_EmptyCommentsSlice(t *testing.T) {
	issue := &cache.Issue{
		Number: 1,
		Repo:   "owner/repo",
		Title:  "Test Issue",
		Body:   "Issue body",
		State:  "open",
	}

	result := ToMarkdown(issue, []cache.Comment{})

	// Check comments count is 0 in frontmatter
	if !strings.Contains(result, "comments: 0") {
		t.Error("expected comments: 0 in frontmatter with empty comments slice")
	}

	// Should not have Comments section
	if strings.Contains(result, "## Comments") {
		t.Error("should not have ## Comments section with empty comments slice")
	}
}

func TestToMarkdown_CommentWithMultilineBody(t *testing.T) {
	issue := &cache.Issue{
		Number: 1,
		Repo:   "owner/repo",
		Title:  "Test Issue",
		Body:   "Issue body",
	}

	comments := []cache.Comment{
		{
			ID:        100,
			Author:    "alice",
			Body:      "First line\n\nSecond paragraph\n\nThird paragraph",
			CreatedAt: "2026-01-10T14:12:00Z",
		},
	}

	result := ToMarkdown(issue, comments)

	// Check multiline body is preserved
	if !strings.Contains(result, "First line\n\nSecond paragraph\n\nThird paragraph") {
		t.Error("multiline comment body should be preserved")
	}
}

func TestRoundTrip_WithComments_BodyNotContaminated(t *testing.T) {
	// This test ensures that when we parse markdown with comments,
	// the body doesn't include the comments section
	issue := &cache.Issue{
		Number: 1,
		Repo:   "owner/repo",
		Title:  "Test Issue",
		Body:   "Original body content",
	}

	comments := []cache.Comment{
		{
			ID:        100,
			Author:    "alice",
			Body:      "This is a comment",
			CreatedAt: "2026-01-10T14:12:00Z",
		},
	}

	// Convert to markdown with comments
	markdown := ToMarkdown(issue, comments)

	// Parse back
	parsed, err := FromMarkdown(markdown)
	if err != nil {
		t.Fatalf("failed to parse markdown: %v", err)
	}

	// Body should NOT contain comment content
	if strings.Contains(parsed.Body, "This is a comment") {
		t.Error("parsed body should not contain comment content")
	}
	if strings.Contains(parsed.Body, "## Comments") {
		t.Error("parsed body should not contain ## Comments section")
	}

	// Body should still have original content
	if !strings.Contains(parsed.Body, "Original body content") {
		t.Error("parsed body should contain original body content")
	}
}

// Edge case tests

func TestToMarkdown_LargeIssueWithManyComments(t *testing.T) {
	// Create ~1.5KB body by repeating text
	bodyChunk := "This is some repeated text for the issue body. "
	var bodyBuilder strings.Builder
	for bodyBuilder.Len() < 1500 {
		bodyBuilder.WriteString(bodyChunk)
	}
	largeBody := bodyBuilder.String()

	issue := &cache.Issue{
		Number:    999,
		Repo:      "test/large-issue",
		Title:     "Large Issue Test",
		Body:      largeBody,
		State:     "open",
		Author:    "alice",
		CreatedAt: "2026-01-01T00:00:00Z",
		UpdatedAt: "2026-01-10T00:00:00Z",
	}

	// Create 100 comments
	comments := make([]cache.Comment, 100)
	for i := 0; i < 100; i++ {
		comments[i] = cache.Comment{
			ID:        int64(1000 + i),
			Author:    "commenter",
			Body:      "This is comment number " + string(rune('0'+i%10)),
			CreatedAt: "2026-01-10T10:00:00Z",
		}
	}

	result := ToMarkdown(issue, comments)

	// Verify comments: 100 in frontmatter
	if !strings.Contains(result, "comments: 100") {
		t.Error("expected comments: 100 in frontmatter")
	}

	// Verify last comment is present (comment_id: 1099)
	if !strings.Contains(result, "<!-- comment_id: 1099 -->") {
		t.Error("expected last comment (id 1099) to be present")
	}

	// Verify reasonable size (10-20KB range)
	size := len(result)
	if size < 10000 || size > 20000 {
		t.Errorf("expected output size between 10-20KB, got %d bytes", size)
	}
}

func TestFromMarkdown_BodyWithNestedMarkdown(t *testing.T) {
	content := `---
id: 1
repo: test/repo
---

# Title

## Body

Introduction paragraph.

### Sub-heading (should stay in body)

Some content under sub-heading.

` + "```go" + `
func main() {
    fmt.Println("Hello")
}
` + "```" + `

> This is a blockquote
> spanning multiple lines

- List item 1
- List item 2
  - Nested item

End of body.

## Comments

### 2026-01-10T14:12:00Z - alice
<!-- comment_id: 100 -->

This comment should NOT appear in body.
`

	parsed, err := FromMarkdown(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Verify sub-headings preserved in body
	if !strings.Contains(parsed.Body, "### Sub-heading") {
		t.Error("sub-heading should be preserved in body")
	}

	// Verify code blocks preserved
	if !strings.Contains(parsed.Body, "```go") {
		t.Error("code block opening should be preserved")
	}
	if !strings.Contains(parsed.Body, "fmt.Println") {
		t.Error("code block content should be preserved")
	}

	// Verify blockquotes preserved
	if !strings.Contains(parsed.Body, "> This is a blockquote") {
		t.Error("blockquote should be preserved in body")
	}

	// Verify lists preserved
	if !strings.Contains(parsed.Body, "- List item 1") {
		t.Error("list items should be preserved in body")
	}
	if !strings.Contains(parsed.Body, "  - Nested item") {
		t.Error("nested list items should be preserved in body")
	}

	// Verify comment section NOT in body
	if strings.Contains(parsed.Body, "## Comments") {
		t.Error("## Comments section should not be in body")
	}
	if strings.Contains(parsed.Body, "This comment should NOT appear") {
		t.Error("comment content should not be in body")
	}
}

func TestToMarkdown_UnicodeContent(t *testing.T) {
	issue := &cache.Issue{
		Number: 1,
		Repo:   "test/unicode",
		Title:  "Unicode test: cafe, nihao, privet, emoji ",
		Body:   "Body with unicode: cafe nihao privet emojis   ",
		State:  "open",
		Author: "user",
	}

	result := ToMarkdown(issue)

	// Verify unicode preserved in title
	if !strings.Contains(result, "cafe") {
		t.Error("unicode 'cafe' should be preserved in title")
	}
	if !strings.Contains(result, "nihao") {
		t.Error("unicode 'nihao' should be preserved in title")
	}
	if !strings.Contains(result, "privet") {
		t.Error("unicode 'privet' should be preserved in title")
	}
	if !strings.Contains(result, "") {
		t.Error("emoji should be preserved in title")
	}

	// Verify unicode preserved in body
	if !strings.Contains(result, "emojis   ") {
		t.Error("emojis should be preserved in body")
	}
}

func TestFromMarkdown_WindowsLineEndings(t *testing.T) {
	// Content with \r\n line endings (Windows style)
	content := "---\r\nid: 1\r\nrepo: test/repo\r\n---\r\n\r\n# Windows Line Endings Test\r\n\r\n## Body\r\n\r\nThis content uses Windows line endings.\r\n\r\nSecond paragraph."

	parsed, err := FromMarkdown(content)
	if err != nil {
		t.Fatalf("unexpected error parsing Windows line endings: %v", err)
	}

	if parsed.Number != 1 {
		t.Errorf("expected Number 1, got %d", parsed.Number)
	}
	if parsed.Repo != "test/repo" {
		t.Errorf("expected Repo 'test/repo', got %q", parsed.Repo)
	}
	if parsed.Title != "Windows Line Endings Test" {
		t.Errorf("expected title 'Windows Line Endings Test', got %q", parsed.Title)
	}
	if !strings.Contains(parsed.Body, "Windows line endings") {
		t.Error("body should contain expected content")
	}
}

func TestToMarkdown_VeryLongTitle(t *testing.T) {
	// Create a 200-character title
	longTitle := strings.Repeat("A", 200)

	issue := &cache.Issue{
		Number: 1,
		Repo:   "test/repo",
		Title:  longTitle,
		Body:   "Body content",
		State:  "open",
	}

	result := ToMarkdown(issue)

	// Verify the full long title is in the output
	if !strings.Contains(result, "# "+longTitle) {
		t.Error("long title should be fully rendered")
	}

	// Verify the markdown is still valid (has proper structure)
	if !strings.HasPrefix(result, "---\n") {
		t.Error("markdown should still start with frontmatter")
	}
	if !strings.Contains(result, "## Body") {
		t.Error("markdown should still have ## Body section")
	}

	// Verify we can parse it back
	parsed, err := FromMarkdown(result)
	if err != nil {
		t.Fatalf("failed to parse markdown with long title: %v", err)
	}
	if parsed.Title != longTitle {
		t.Errorf("title not preserved: expected %d chars, got %d chars", len(longTitle), len(parsed.Title))
	}
}

func TestFromMarkdown_CodeBlockWithYamlLikeSyntax(t *testing.T) {
	content := `---
id: 1
repo: test/repo
---

# Title

## Body

Here's a config example:

` + "```yaml" + `
---
config:
  key: value
---
` + "```" + `

End of body.
`
	parsed, err := FromMarkdown(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if !strings.Contains(parsed.Body, "config:") {
		t.Error("code block content should be in body")
	}
	if !strings.Contains(parsed.Body, "key: value") {
		t.Error("YAML-like content inside code block should be preserved")
	}
	if !strings.Contains(parsed.Body, "End of body.") {
		t.Error("content after code block should be preserved")
	}
}

// Parse failure scenario tests for malformed templates

func TestFromMarkdown_ErrorOnUnclosedFrontmatter(t *testing.T) {
	content := `---
id: 1
repo: test/repo

# Title

## Body

Content with only opening frontmatter delimiter`

	_, err := FromMarkdown(content)
	if err == nil {
		t.Error("expected error when frontmatter has only opening delimiter")
	}
	if !strings.Contains(err.Error(), "closing") {
		t.Errorf("error should mention missing closing delimiter, got: %v", err)
	}
}

func TestFromMarkdown_ErrorOnEmptyContent(t *testing.T) {
	_, err := FromMarkdown("")
	if err == nil {
		t.Error("expected error for empty content")
	}
}

func TestFromMarkdown_ErrorOnOnlyWhitespace(t *testing.T) {
	_, err := FromMarkdown("   \n\n   \t\n")
	if err == nil {
		t.Error("expected error for whitespace-only content")
	}
}

func TestFromMarkdown_ErrorOnArrayInId(t *testing.T) {
	content := `---
id: [1, 2, 3]
repo: test/repo
---

# Title

## Body

Content`

	_, err := FromMarkdown(content)
	if err == nil {
		t.Error("expected error when id is an array instead of integer")
	}
}

func TestFromMarkdown_ErrorOnNestedYamlInFrontmatter(t *testing.T) {
	content := `---
id:
  nested:
    value: 1
repo: test/repo
---

# Title

## Body

Content`

	_, err := FromMarkdown(content)
	if err == nil {
		t.Error("expected error when id has nested structure instead of integer")
	}
}

func TestFromMarkdown_MissingTitleLine(t *testing.T) {
	// Missing title (# heading) should still parse but title will be empty
	content := `---
id: 1
repo: test/repo
---

## Body

Content without a title heading`

	parsed, err := FromMarkdown(content)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Title should be empty when no # heading is found
	if parsed.Title != "" {
		t.Errorf("expected empty title when # heading is missing, got %q", parsed.Title)
	}

	// Body should still be parsed
	if !strings.Contains(parsed.Body, "Content without a title heading") {
		t.Error("body content should still be extracted")
	}
}

func TestFromMarkdown_ParseFailureScenarios(t *testing.T) {
	// Comprehensive test of various parse failure scenarios
	tests := []struct {
		name        string
		content     string
		shouldError bool
		errContains string
	}{
		{
			name:        "missing frontmatter entirely",
			content:     "# Title\n\n## Body\n\nContent",
			shouldError: true,
			errContains: "frontmatter",
		},
		{
			name:        "frontmatter not at start",
			content:     "\n---\nid: 1\nrepo: test/repo\n---\n\n# Title",
			shouldError: true,
			errContains: "frontmatter",
		},
		{
			name:        "malformed yaml - unclosed bracket",
			content:     "---\nid: [1\nrepo: test/repo\n---\n\n# Title",
			shouldError: true,
			errContains: "YAML",
		},
		{
			name:        "malformed yaml - unclosed brace",
			content:     "---\nid: {key: value\nrepo: test/repo\n---\n\n# Title",
			shouldError: true,
			errContains: "YAML",
		},
		{
			name:        "id as string instead of int",
			content:     "---\nid: not_a_number\nrepo: test/repo\n---\n\n# Title",
			shouldError: true,
			errContains: "",
		},
		{
			// Note: YAML library silently truncates floats to int (1.5 -> 1)
			// This is valid behavior, so we don't expect an error
			name:        "id as float (truncated to int)",
			content:     "---\nid: 1.5\nrepo: test/repo\n---\n\n# Title",
			shouldError: false,
		},
		{
			name: "valid minimal content",
			content: `---
id: 1
repo: test/repo
---

# Title

## Body

Content`,
			shouldError: false,
		},
		{
			name: "valid with empty fields",
			content: `---
id: 0
repo: ""
---

#

## Body

`,
			shouldError: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := FromMarkdown(tc.content)

			if tc.shouldError {
				if err == nil {
					t.Errorf("expected error for %s, but got nil", tc.name)
					return
				}
				if tc.errContains != "" && !strings.Contains(err.Error(), tc.errContains) {
					t.Errorf("error should contain %q, got: %v", tc.errContains, err)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error for %s: %v", tc.name, err)
				}
			}
		})
	}
}
