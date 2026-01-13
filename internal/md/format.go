// Package md provides markdown formatting and parsing for GitHub issues.
package md

import (
	"fmt"
	"regexp"
	"strconv"
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
	State    string
	Author   string
	ETag     string
	Comments []ParsedComment
}

// ParsedComment represents a comment parsed from markdown.
type ParsedComment struct {
	ID     int64  // 0 for new comments, >0 for existing
	Author string // parsed from header, may be empty for new comments
	Body   string
	IsNew  bool // true if this is a new comment (no valid comment_id)
}

// Changes indicates what was modified between original and parsed issue.
type Changes struct {
	TitleChanged    bool
	BodyChanged     bool
	NewTitle        string
	NewBody         string
	CommentChanges  []CommentChange
	NewComments     []ParsedComment // Comments with IsNew=true
	EditedComments  []CommentChange // Existing comments that were modified
}

// CommentChange represents a change to an existing comment.
type CommentChange struct {
	ID      int64
	NewBody string
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

	// Extract comments from ## Comments section
	comments := extractComments(remaining)
	parsed.Comments = comments

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

// DetectCommentChanges compares parsed comments with original cached comments.
// Returns new comments and edited comments separately.
func DetectCommentChanges(originalComments []cache.Comment, parsedComments []ParsedComment) (newComments []ParsedComment, editedComments []CommentChange) {
	// Build a map of original comment IDs to their bodies
	originalByID := make(map[int64]string)
	for _, c := range originalComments {
		originalByID[c.ID] = c.Body
	}

	for _, pc := range parsedComments {
		if pc.IsNew || pc.ID == 0 {
			// This is a new comment
			if strings.TrimSpace(pc.Body) != "" {
				newComments = append(newComments, pc)
			}
		} else {
			// Existing comment - check if body changed
			origBody, exists := originalByID[pc.ID]
			if exists {
				// Normalize bodies for comparison
				origNorm := strings.TrimRight(origBody, "\n\r")
				parsedNorm := strings.TrimRight(pc.Body, "\n\r")
				if origNorm != parsedNorm {
					editedComments = append(editedComments, CommentChange{
						ID:      pc.ID,
						NewBody: pc.Body,
					})
				}
			}
			// If ID doesn't exist in original, ignore (orphaned comment reference)
		}
	}

	return newComments, editedComments
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

// commentHeaderRegex matches comment headers: ### timestamp - author
var commentHeaderRegex = regexp.MustCompile(`(?m)^### (.+?) - (.+)$`)

// commentIDRegex matches the comment_id HTML comment: <!-- comment_id: 123 -->
var commentIDRegex = regexp.MustCompile(`<!--\s*comment_id:\s*(\w+)\s*-->`)

// extractComments parses the ## Comments section and extracts individual comments.
// Comments are expected in the format:
//
//	### 2026-01-10T14:12:00Z - alice
//	<!-- comment_id: 987654 -->
//
//	Comment body here.
//
// For new comments, the header can be:
//
//	### new
//	<!-- comment_id: new -->
//
//	New comment body.
func extractComments(content string) []ParsedComment {
	// Find ## Comments section
	commentsPattern := regexp.MustCompile(`(?m)^## Comments\s*$`)
	loc := commentsPattern.FindStringIndex(content)
	if loc == nil {
		return nil
	}

	// Start after the ## Comments line
	afterHeader := content[loc[1]:]

	// Skip leading newline
	if len(afterHeader) > 0 && afterHeader[0] == '\n' {
		afterHeader = afterHeader[1:]
	}

	// Find the next ## heading (if any, to delimit the comments section)
	nextSectionPattern := regexp.MustCompile(`(?m)^## [^#]`)
	nextLoc := nextSectionPattern.FindStringIndex(afterHeader)
	var commentsSection string
	if nextLoc != nil {
		commentsSection = afterHeader[:nextLoc[0]]
	} else {
		commentsSection = afterHeader
	}

	// Split into individual comments by ### headers
	// Each comment starts with ### (timestamp - author) or ### new
	commentBlocks := splitCommentBlocks(commentsSection)

	var comments []ParsedComment
	for _, block := range commentBlocks {
		comment := parseCommentBlock(block)
		if comment != nil {
			comments = append(comments, *comment)
		}
	}

	return comments
}

// splitCommentBlocks splits the comments section into individual comment blocks.
// Each block starts with ### and ends before the next ### or end of content.
func splitCommentBlocks(content string) []string {
	headerPattern := regexp.MustCompile(`(?m)^### `)
	indices := headerPattern.FindAllStringIndex(content, -1)

	if len(indices) == 0 {
		return nil
	}

	var blocks []string
	for i, loc := range indices {
		start := loc[0]
		var end int
		if i+1 < len(indices) {
			end = indices[i+1][0]
		} else {
			end = len(content)
		}
		block := content[start:end]
		blocks = append(blocks, block)
	}

	return blocks
}

// parseCommentBlock parses a single comment block and returns a ParsedComment.
// Returns nil if the block is invalid.
func parseCommentBlock(block string) *ParsedComment {
	lines := strings.SplitN(block, "\n", -1)
	if len(lines) == 0 {
		return nil
	}

	// First line should be the header: ### timestamp - author or ### new
	headerLine := strings.TrimSpace(lines[0])
	if !strings.HasPrefix(headerLine, "### ") {
		return nil
	}

	header := strings.TrimPrefix(headerLine, "### ")
	comment := &ParsedComment{}

	// Check if this is a "new" comment
	if strings.ToLower(strings.TrimSpace(header)) == "new" {
		comment.IsNew = true
		comment.ID = 0
	} else {
		// Try to parse as "timestamp - author"
		matches := commentHeaderRegex.FindStringSubmatch(headerLine)
		if matches != nil && len(matches) >= 3 {
			comment.Author = matches[2]
		}
	}

	// Look for comment_id in the remaining lines
	var bodyStartIdx int
	for i, line := range lines[1:] {
		trimmed := strings.TrimSpace(line)

		// Look for comment_id HTML comment
		if idMatch := commentIDRegex.FindStringSubmatch(trimmed); idMatch != nil {
			idStr := idMatch[1]
			if strings.ToLower(idStr) == "new" {
				comment.IsNew = true
				comment.ID = 0
			} else {
				id, err := strconv.ParseInt(idStr, 10, 64)
				if err == nil {
					comment.ID = id
				} else {
					// Invalid ID format, treat as new
					comment.IsNew = true
					comment.ID = 0
				}
			}
			bodyStartIdx = i + 2 // Skip to line after comment_id
			continue
		}

		// Empty lines before body
		if trimmed == "" && bodyStartIdx == 0 {
			continue
		}

		// Start of body content
		if bodyStartIdx == 0 && trimmed != "" {
			bodyStartIdx = i + 1
		}
	}

	// Extract body from the remaining lines
	if bodyStartIdx > 0 && bodyStartIdx < len(lines) {
		bodyLines := lines[bodyStartIdx:]
		// Skip leading empty lines
		for len(bodyLines) > 0 && strings.TrimSpace(bodyLines[0]) == "" {
			bodyLines = bodyLines[1:]
		}
		body := strings.Join(bodyLines, "\n")
		body = strings.TrimRight(body, "\n\r")
		comment.Body = body
	}

	return comment
}
