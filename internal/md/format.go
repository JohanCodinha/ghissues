// Package md provides markdown formatting and parsing for GitHub issues.
package md

// Issue represents issue data for markdown conversion.
type Issue struct {
	Number    int
	Repo      string
	URL       string
	State     string
	Author    string
	CreatedAt string
	UpdatedAt string
	ETag      string
	Title     string
	Body      string
}

// Format converts an issue to markdown format with YAML frontmatter.
func Format(issue Issue) string {
	// TODO: Implement issue to markdown conversion
	return ""
}

// Parse parses markdown content back into issue data.
func Parse(content string) (*Issue, error) {
	// TODO: Implement markdown to issue parsing
	return nil, nil
}
