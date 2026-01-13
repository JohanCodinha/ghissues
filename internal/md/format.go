// Package md provides markdown formatting and parsing for GitHub issues.
package md

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/JohanCodinha/ghissues/internal/cache"
	"gopkg.in/yaml.v3"
)

// ParsedIssue represents data extracted from a markdown file.
type ParsedIssue struct {
	Number int
	Repo   string
	Title  string
	Body   string
	// Frontmatter fields for reference
	State  string
	Author string
	ETag   string
}

// Changes indicates what was modified between original and parsed issue.
type Changes struct {
	TitleChanged bool
	BodyChanged  bool
	NewTitle     string
	NewBody      string
}

// frontmatter represents the YAML frontmatter structure.
type frontmatter struct {
	ID        int      `yaml:"id"`
	Repo      string   `yaml:"repo"`
	URL       string   `yaml:"url"`
	State     string   `yaml:"state"`
	Labels    []string `yaml:"labels,omitempty,flow"`
	Author    string   `yaml:"author"`
	CreatedAt string   `yaml:"created_at"`
	UpdatedAt string   `yaml:"updated_at"`
	ETag      string   `yaml:"etag"`
	Comments  int      `yaml:"comments"`
}

// ToMarkdown converts a cache.Issue to markdown format with YAML frontmatter.
// If comments are provided, they are included in a ## Comments section.
func ToMarkdown(issue *cache.Issue, comments ...[]cache.Comment) string {
	var sb strings.Builder

	// Get comments if provided
	var issueComments []cache.Comment
	if len(comments) > 0 {
		issueComments = comments[0]
	}

	// Build frontmatter
	fm := frontmatter{
		ID:        issue.Number,
		Repo:      issue.Repo,
		URL:       fmt.Sprintf("https://github.com/%s/issues/%d", issue.Repo, issue.Number),
		State:     issue.State,
		Labels:    issue.Labels,
		Author:    issue.Author,
		CreatedAt: issue.CreatedAt,
		UpdatedAt: issue.UpdatedAt,
		ETag:      issue.ETag,
		Comments:  len(issueComments),
	}

	// Marshal frontmatter to YAML
	yamlBytes, err := yaml.Marshal(&fm)
	if err != nil {
		// Fallback to minimal frontmatter on error
		sb.WriteString("---\n")
		sb.WriteString(fmt.Sprintf("id: %d\n", issue.Number))
		sb.WriteString(fmt.Sprintf("repo: %s\n", issue.Repo))
		sb.WriteString("---\n")
	} else {
		sb.WriteString("---\n")
		sb.Write(yamlBytes)
		sb.WriteString("---\n")
	}

	// Add title
	sb.WriteString("\n# ")
	sb.WriteString(issue.Title)
	sb.WriteString("\n")

	// Add body section
	sb.WriteString("\n## Body\n\n")
	sb.WriteString(issue.Body)

	// Ensure body section ends with newline
	if len(issue.Body) > 0 && !strings.HasSuffix(issue.Body, "\n") {
		sb.WriteString("\n")
	}

	// Add comments section if there are comments
	if len(issueComments) > 0 {
		sb.WriteString("\n## Comments\n")

		for _, comment := range issueComments {
			// Format: ### 2026-01-10T14:12:00Z - username
			sb.WriteString("\n### ")
			sb.WriteString(comment.CreatedAt)
			sb.WriteString(" - ")
			sb.WriteString(comment.Author)
			sb.WriteString("\n")

			// Add comment_id HTML comment
			sb.WriteString(fmt.Sprintf("<!-- comment_id: %d -->\n", comment.ID))

			// Add comment body
			sb.WriteString("\n")
			sb.WriteString(comment.Body)

			// Ensure comment body ends with newline
			if len(comment.Body) > 0 && !strings.HasSuffix(comment.Body, "\n") {
				sb.WriteString("\n")
			}
		}
	}

	return sb.String()
}

// FromMarkdown parses markdown content and extracts issue data.
// Returns an error if the content is malformed or missing required fields.
func FromMarkdown(content string) (*ParsedIssue, error) {
	parsed := &ParsedIssue{}

	// Extract frontmatter
	fm, remaining, err := extractFrontmatter(content)
	if err != nil {
		return nil, fmt.Errorf("failed to parse frontmatter: %w", err)
	}

	// Populate from frontmatter
	parsed.Number = fm.ID
	parsed.Repo = fm.Repo
	parsed.State = fm.State
	parsed.Author = fm.Author
	parsed.ETag = fm.ETag

	// Extract title from # heading
	title, remaining := extractTitle(remaining)
	parsed.Title = title

	// Extract body from ## Body section
	body := extractBody(remaining)
	parsed.Body = body

	return parsed, nil
}

// DetectChanges compares an original issue with a parsed issue and returns what changed.
func DetectChanges(original *cache.Issue, parsed *ParsedIssue) Changes {
	changes := Changes{}

	// Compare title
	if original.Title != parsed.Title {
		changes.TitleChanged = true
		changes.NewTitle = parsed.Title
	}

	// Compare body - normalize trailing whitespace for comparison
	originalBody := strings.TrimRight(original.Body, "\n")
	parsedBody := strings.TrimRight(parsed.Body, "\n")

	if originalBody != parsedBody {
		changes.BodyChanged = true
		changes.NewBody = parsed.Body
	}

	return changes
}

// extractFrontmatter parses YAML frontmatter from markdown content.
// Returns the parsed frontmatter, remaining content, and any error.
func extractFrontmatter(content string) (*frontmatter, string, error) {
	// Check for frontmatter delimiter
	if !strings.HasPrefix(content, "---") {
		return nil, content, fmt.Errorf("missing frontmatter: content must start with ---")
	}

	// Find the closing delimiter
	rest := content[3:] // Skip opening ---

	// Skip any whitespace/newline after opening ---
	rest = strings.TrimLeft(rest, " \t")
	if len(rest) > 0 && rest[0] == '\n' {
		rest = rest[1:]
	} else if len(rest) > 1 && rest[0] == '\r' && rest[1] == '\n' {
		rest = rest[2:]
	}

	// Find closing ---
	endIdx := strings.Index(rest, "\n---")
	if endIdx == -1 {
		// Try with just --- at start of remaining content
		if strings.HasPrefix(rest, "---") {
			// Empty frontmatter
			return &frontmatter{}, strings.TrimPrefix(rest, "---"), nil
		}
		return nil, content, fmt.Errorf("missing closing frontmatter delimiter ---")
	}

	yamlContent := rest[:endIdx]
	remaining := rest[endIdx+4:] // Skip \n---

	// Skip newline after closing ---
	if len(remaining) > 0 && remaining[0] == '\n' {
		remaining = remaining[1:]
	} else if len(remaining) > 1 && remaining[0] == '\r' && remaining[1] == '\n' {
		remaining = remaining[2:]
	}

	// Parse YAML
	var fm frontmatter
	if err := yaml.Unmarshal([]byte(yamlContent), &fm); err != nil {
		return nil, content, fmt.Errorf("invalid YAML in frontmatter: %w", err)
	}

	return &fm, remaining, nil
}

// extractTitle extracts the title from a # heading line.
// Returns the title and remaining content after the title line.
func extractTitle(content string) (string, string) {
	lines := strings.SplitN(content, "\n", -1)

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "# ") {
			title := strings.TrimPrefix(trimmed, "# ")
			// Return remaining content after this line
			remaining := strings.Join(lines[i+1:], "\n")
			return title, remaining
		}
	}

	return "", content
}

// extractBody extracts the body content from the ## Body section.
// The body section ends at the next ## heading or end of file.
func extractBody(content string) string {
	// Find ## Body section
	bodyPattern := regexp.MustCompile(`(?m)^## Body\s*$`)
	loc := bodyPattern.FindStringIndex(content)
	if loc == nil {
		// No ## Body section found - return empty
		return ""
	}

	// Start after the ## Body line
	afterHeader := content[loc[1]:]

	// Skip leading newline after ## Body
	if len(afterHeader) > 0 && afterHeader[0] == '\n' {
		afterHeader = afterHeader[1:]
	} else if len(afterHeader) > 1 && afterHeader[0] == '\r' && afterHeader[1] == '\n' {
		afterHeader = afterHeader[2:]
	}

	// Find next ## heading (end of body section)
	nextSectionPattern := regexp.MustCompile(`(?m)^## `)
	nextLoc := nextSectionPattern.FindStringIndex(afterHeader)

	var body string
	if nextLoc != nil {
		body = afterHeader[:nextLoc[0]]
	} else {
		body = afterHeader
	}

	// Trim trailing newlines but preserve internal structure
	body = strings.TrimRight(body, "\n\r")

	return body
}
