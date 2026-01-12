// Package gh provides a GitHub API client for interacting with issues.
package gh

// Issue represents a GitHub issue.
type Issue struct {
	Number    int
	Title     string
	Body      string
	State     string
	Author    string
	CreatedAt string
	UpdatedAt string
	ETag      string
}

// Client is a GitHub API client.
type Client struct {
	token string
	owner string
	repo  string
}

// New creates a new GitHub API client.
func New(token, owner, repo string) *Client {
	return &Client{
		token: token,
		owner: owner,
		repo:  repo,
	}
}

// ListIssues fetches all issues from the repository.
func (c *Client) ListIssues() ([]Issue, error) {
	// TODO: Implement GitHub API call
	return nil, nil
}

// GetIssue fetches a single issue by number.
func (c *Client) GetIssue(number int) (*Issue, string, error) {
	// TODO: Implement GitHub API call
	return nil, "", nil
}

// UpdateIssue updates an issue's body.
func (c *Client) UpdateIssue(number int, body string) error {
	// TODO: Implement GitHub API call
	return nil
}
