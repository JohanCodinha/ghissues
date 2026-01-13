// Package gh provides a GitHub API client for interacting with issues.
package gh

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/JohanCodinha/ghissues/internal/logger"
	"gopkg.in/yaml.v3"
)

const (
	apiBaseURL = "https://api.github.com"
)

// sleepFunc is the function used for sleeping. It can be replaced in tests.
var sleepFunc = time.Sleep

// Label represents a GitHub issue label.
type Label struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// User represents a GitHub user.
type User struct {
	Login string `json:"login"`
}

// SubIssuesSummary contains summary info about an issue's sub-issues.
type SubIssuesSummary struct {
	Total            int `json:"total"`
	Completed        int `json:"completed"`
	PercentCompleted int `json:"percent_completed"`
}

// Issue represents a GitHub issue.
type Issue struct {
	Number           int               `json:"number"`
	ID               int64             `json:"id"` // Numeric ID needed for sub-issues API
	Title            string            `json:"title"`
	Body             string            `json:"body"`
	State            string            `json:"state"`
	Labels           []Label           `json:"labels"`
	User             User              `json:"user"`
	CreatedAt        time.Time         `json:"created_at"`
	UpdatedAt        time.Time         `json:"updated_at"`
	ETag             string            `json:"-"` // Not from JSON, set from response header
	ParentIssueURL   string            `json:"parent_issue_url,omitempty"`
	SubIssuesSummary *SubIssuesSummary `json:"sub_issues_summary,omitempty"`
}

// Comment represents a GitHub issue comment.
type Comment struct {
	ID        int64     `json:"id"`
	User      User      `json:"user"`
	Body      string    `json:"body"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

// Client is a GitHub API client.
type Client struct {
	token      string
	baseURL    string
	httpClient *http.Client
}

// ghHostsConfig represents the structure of ~/.config/gh/hosts.yml
type ghHostsConfig map[string]ghHost

type ghHost struct {
	OAuthToken string `yaml:"oauth_token"`
	User       string `yaml:"user"`
}

// New creates a new GitHub API client with the given token.
func New(token string) *Client {
	return &Client{
		token:      token,
		baseURL:    apiBaseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// NewWithBaseURL creates a GitHub API client with a custom base URL (for testing).
func NewWithBaseURL(token, baseURL string) *Client {
	return &Client{
		token:      token,
		baseURL:    baseURL,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// GetToken attempts to get a GitHub token from various sources:
// 1. Run `gh auth token` command (gh CLI with keyring storage)
// 2. Read from ~/.config/gh/hosts.yml (older gh CLI format)
// 3. GITHUB_TOKEN environment variable
func GetToken() (string, error) {
	// Try gh auth token command first (handles keyring storage)
	if token, err := getTokenFromGhCLI(); err == nil && token != "" {
		return token, nil
	}

	// Try reading from gh hosts.yml config (older format)
	if token, err := getTokenFromGhConfig(); err == nil && token != "" {
		return token, nil
	}

	// Fall back to GITHUB_TOKEN env var
	if token := os.Getenv("GITHUB_TOKEN"); token != "" {
		return token, nil
	}

	return "", fmt.Errorf("no GitHub token found: install gh CLI and run 'gh auth login', or set GITHUB_TOKEN env var")
}

// getTokenFromGhCLI runs `gh auth token` to get the token from the gh CLI.
func getTokenFromGhCLI() (string, error) {
	cmd := exec.Command("gh", "auth", "token")
	output, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("gh auth token failed: %w", err)
	}
	return strings.TrimSpace(string(output)), nil
}

// getTokenFromGhConfig reads the token from ~/.config/gh/hosts.yml.
func getTokenFromGhConfig() (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	configPath := filepath.Join(homeDir, ".config", "gh", "hosts.yml")
	return getTokenFromGhConfigPath(configPath)
}

// getTokenFromGhConfigPath reads the token from the specified hosts.yml path.
// This is split out from getTokenFromGhConfig for testability.
func getTokenFromGhConfigPath(configPath string) (string, error) {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return "", fmt.Errorf("failed to read gh config: %w", err)
	}

	var config ghHostsConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return "", fmt.Errorf("failed to parse gh config: %w", err)
	}

	// Look for github.com host
	if host, ok := config["github.com"]; ok {
		if host.OAuthToken != "" {
			return host.OAuthToken, nil
		}
	}

	return "", fmt.Errorf("no oauth_token found in gh config")
}

// doRequest performs an HTTP request with authentication and returns the response.
// Handles 429 rate limit responses by sleeping until reset time and retrying.
func (c *Client) doRequest(method, url string, body io.Reader) (*http.Response, error) {
	// If body is a bytes.Reader, we can retry by seeking back to start
	var bodyBytes []byte
	if body != nil {
		var err error
		bodyBytes, err = io.ReadAll(body)
		if err != nil {
			return nil, fmt.Errorf("failed to read request body: %w", err)
		}
	}

	for {
		var reqBody io.Reader
		if bodyBytes != nil {
			reqBody = bytes.NewReader(bodyBytes)
		}

		req, err := http.NewRequest(method, url, reqBody)
		if err != nil {
			return nil, fmt.Errorf("failed to create request: %w", err)
		}

		req.Header.Set("Authorization", "Bearer "+c.token)
		req.Header.Set("Accept", "application/vnd.github+json")
		req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

		if bodyBytes != nil {
			req.Header.Set("Content-Type", "application/json")
		}

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("request failed: %w", err)
		}

		// Handle rate limiting - sleep and retry
		if resp.StatusCode == http.StatusTooManyRequests {
			resp.Body.Close()
			if checkRateLimit(resp) {
				continue // Retry after sleeping
			}
			// If checkRateLimit didn't sleep (no valid reset header), wait 60s
			logger.Warn("rate limited without reset header, waiting 60s")
			sleepFunc(60 * time.Second)
			continue
		}

		return resp, nil
	}
}

// checkRateLimit checks rate limit headers and sleeps if rate limited.
// Returns true if we were rate limited and slept (caller should retry).
func checkRateLimit(resp *http.Response) bool {
	remaining := resp.Header.Get("X-RateLimit-Remaining")
	reset := resp.Header.Get("X-RateLimit-Reset")

	if remaining == "0" && reset != "" {
		resetTime, err := strconv.ParseInt(reset, 10, 64)
		if err == nil {
			resetAt := time.Unix(resetTime, 0)
			sleepDuration := time.Until(resetAt)
			if sleepDuration > 0 {
				logger.Warn("rate limited, sleeping %v until %s", sleepDuration, resetAt.Format(time.RFC3339))
				sleepFunc(sleepDuration)
				return true
			}
		}
	}
	return false
}

// ListIssues fetches all open issues from the repository.
// Handles pagination automatically.
func (c *Client) ListIssues(owner, repo string) ([]Issue, error) {
	var allIssues []Issue
	url := fmt.Sprintf("%s/repos/%s/%s/issues?state=all&per_page=100", c.baseURL, owner, repo)

	for url != "" {
		resp, err := c.doRequest("GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to list issues for %s/%s: %w", owner, repo, err)
		}

		checkRateLimit(resp)

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("failed to list issues for %s/%s: API error %s - %s", owner, repo, resp.Status, string(body))
		}

		var issues []Issue
		if err := json.NewDecoder(resp.Body).Decode(&issues); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("failed to decode issues response for %s/%s: %w", owner, repo, err)
		}

		// Parse Link header for pagination before closing
		url = getNextPageURL(resp.Header.Get("Link"))

		// Close response body immediately after reading
		resp.Body.Close()

		allIssues = append(allIssues, issues...)
	}

	return allIssues, nil
}

// getNextPageURL extracts the next page URL from the Link header.
// Link header format: <url>; rel="next", <url>; rel="last"
func getNextPageURL(linkHeader string) string {
	if linkHeader == "" {
		return ""
	}

	// Match <url>; rel="next"
	re := regexp.MustCompile(`<([^>]+)>;\s*rel="next"`)
	matches := re.FindStringSubmatch(linkHeader)
	if len(matches) >= 2 {
		return matches[1]
	}

	return ""
}

// GetIssue fetches a single issue by number.
// Returns the issue, the ETag header value, and any error.
func (c *Client) GetIssue(owner, repo string, number int) (*Issue, string, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d", c.baseURL, owner, repo, number)

	resp, err := c.doRequest("GET", url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("failed to get issue #%d for %s/%s: %w", number, owner, repo, err)
	}
	defer resp.Body.Close()

	checkRateLimit(resp)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("failed to get issue #%d for %s/%s: API error %s - %s", number, owner, repo, resp.Status, string(body))
	}

	var issue Issue
	if err := json.NewDecoder(resp.Body).Decode(&issue); err != nil {
		return nil, "", fmt.Errorf("failed to decode issue #%d response for %s/%s: %w", number, owner, repo, err)
	}

	etag := resp.Header.Get("ETag")
	issue.ETag = etag

	return &issue, etag, nil
}

// IssueUpdate contains optional fields for updating an issue.
// Nil fields are not included in the update request.
type IssueUpdate struct {
	Title  *string
	Body   *string
	State  *string   // "open" or "closed"
	Labels *[]string // Replace all labels with this list
}

// UpdateIssue updates an issue's fields.
// Only non-nil fields in the update struct are sent to GitHub.
func (c *Client) UpdateIssue(owner, repo string, number int, update IssueUpdate) error {
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d", c.baseURL, owner, repo, number)

	payload := make(map[string]interface{})
	if update.Title != nil {
		payload["title"] = *update.Title
	}
	if update.Body != nil {
		payload["body"] = *update.Body
	}
	if update.State != nil {
		payload["state"] = *update.State
	}
	if update.Labels != nil {
		payload["labels"] = *update.Labels
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	resp, err := c.doRequest("PATCH", url, bytes.NewReader(jsonPayload))
	if err != nil {
		return fmt.Errorf("failed to update issue #%d for %s/%s: %w", number, owner, repo, err)
	}
	defer resp.Body.Close()

	checkRateLimit(resp)

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to update issue #%d for %s/%s: API error %s - %s", number, owner, repo, resp.Status, string(respBody))
	}

	return nil
}

// ListComments fetches all comments for an issue.
// Handles pagination automatically.
func (c *Client) ListComments(owner, repo string, number int) ([]Comment, error) {
	var allComments []Comment
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments?per_page=100", c.baseURL, owner, repo, number)

	for url != "" {
		resp, err := c.doRequest("GET", url, nil)
		if err != nil {
			return nil, fmt.Errorf("failed to list comments for issue #%d in %s/%s: %w", number, owner, repo, err)
		}

		checkRateLimit(resp)

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			resp.Body.Close()
			return nil, fmt.Errorf("failed to list comments for issue #%d in %s/%s: API error %s - %s", number, owner, repo, resp.Status, string(body))
		}

		var comments []Comment
		if err := json.NewDecoder(resp.Body).Decode(&comments); err != nil {
			resp.Body.Close()
			return nil, fmt.Errorf("failed to decode comments response for issue #%d in %s/%s: %w", number, owner, repo, err)
		}

		// Parse Link header for pagination before closing
		url = getNextPageURL(resp.Header.Get("Link"))

		// Close response body immediately after reading
		resp.Body.Close()

		allComments = append(allComments, comments...)
	}

	return allComments, nil
}

// CreateComment creates a new comment on an issue.
// Returns the created comment with its assigned ID.
func (c *Client) CreateComment(owner, repo string, number int, body string) (*Comment, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d/comments", c.baseURL, owner, repo, number)

	payload := map[string]string{"body": body}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	resp, err := c.doRequest("POST", url, bytes.NewReader(jsonPayload))
	if err != nil {
		return nil, fmt.Errorf("failed to create comment on issue #%d in %s/%s: %w", number, owner, repo, err)
	}
	defer resp.Body.Close()

	checkRateLimit(resp)

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to create comment on issue #%d in %s/%s: API error %s - %s", number, owner, repo, resp.Status, string(respBody))
	}

	var comment Comment
	if err := json.NewDecoder(resp.Body).Decode(&comment); err != nil {
		return nil, fmt.Errorf("failed to decode comment response for issue #%d in %s/%s: %w", number, owner, repo, err)
	}

	return &comment, nil
}

// UpdateComment updates an existing comment.
func (c *Client) UpdateComment(owner, repo string, commentID int64, body string) error {
	url := fmt.Sprintf("%s/repos/%s/%s/issues/comments/%d", c.baseURL, owner, repo, commentID)

	payload := map[string]string{"body": body}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	resp, err := c.doRequest("PATCH", url, bytes.NewReader(jsonPayload))
	if err != nil {
		return fmt.Errorf("failed to update comment %d in %s/%s: %w", commentID, owner, repo, err)
	}
	defer resp.Body.Close()

	checkRateLimit(resp)

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to update comment %d in %s/%s: API error %s - %s", commentID, owner, repo, resp.Status, string(respBody))
	}

	return nil
}

// CreateIssue creates a new issue in a repository.
// Returns the created issue with its assigned number.
func (c *Client) CreateIssue(owner, repo, title, body string, labels []string) (*Issue, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/issues", c.baseURL, owner, repo)

	payload := map[string]interface{}{
		"title": title,
		"body":  body,
	}
	if len(labels) > 0 {
		payload["labels"] = labels
	}

	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal payload: %w", err)
	}

	resp, err := c.doRequest("POST", url, bytes.NewReader(jsonPayload))
	if err != nil {
		return nil, fmt.Errorf("failed to create issue in %s/%s: %w", owner, repo, err)
	}
	defer resp.Body.Close()

	checkRateLimit(resp)

	if resp.StatusCode != http.StatusCreated {
		respBody, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to create issue in %s/%s: API error %s - %s", owner, repo, resp.Status, string(respBody))
	}

	var issue Issue
	if err := json.NewDecoder(resp.Body).Decode(&issue); err != nil {
		return nil, fmt.Errorf("failed to decode created issue response for %s/%s: %w", owner, repo, err)
	}

	etag := resp.Header.Get("ETag")
	issue.ETag = etag

	return &issue, nil
}

// GetIssueWithEtag fetches an issue using a conditional request with etag.
// Returns (nil, "", nil) on 304 Not Modified.
// Returns (*Issue, newEtag, nil) on 200 OK with new data.
func (c *Client) GetIssueWithEtag(owner, repo string, number int, etag string) (*Issue, string, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d", c.baseURL, owner, repo, number)

	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, "", fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	// Add conditional request header
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", fmt.Errorf("failed to get issue #%d with etag for %s/%s: %w", number, owner, repo, err)
	}
	defer resp.Body.Close()

	checkRateLimit(resp)

	// 304 Not Modified - issue hasn't changed
	if resp.StatusCode == http.StatusNotModified {
		return nil, "", nil
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("failed to get issue #%d with etag for %s/%s: API error %s - %s", number, owner, repo, resp.Status, string(body))
	}

	var issue Issue
	if err := json.NewDecoder(resp.Body).Decode(&issue); err != nil {
		return nil, "", fmt.Errorf("failed to decode issue #%d response for %s/%s: %w", number, owner, repo, err)
	}

	newEtag := resp.Header.Get("ETag")
	issue.ETag = newEtag

	return &issue, newEtag, nil
}

// ListSubIssues fetches all sub-issues for an issue.
func (c *Client) ListSubIssues(owner, repo string, number int) ([]Issue, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d/sub_issues", c.baseURL, owner, repo, number)

	resp, err := c.doRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to list sub-issues for issue #%d in %s/%s: %w", number, owner, repo, err)
	}
	defer resp.Body.Close()

	checkRateLimit(resp)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to list sub-issues for issue #%d in %s/%s: API error %s - %s", number, owner, repo, resp.Status, string(body))
	}

	var subIssues []Issue
	if err := json.NewDecoder(resp.Body).Decode(&subIssues); err != nil {
		return nil, fmt.Errorf("failed to decode sub-issues response for issue #%d in %s/%s: %w", number, owner, repo, err)
	}

	return subIssues, nil
}

// GetParentIssue fetches the parent issue for a sub-issue.
// Returns nil if the issue has no parent.
func (c *Client) GetParentIssue(owner, repo string, number int) (*Issue, error) {
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d/parent", c.baseURL, owner, repo, number)

	resp, err := c.doRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to get parent issue for #%d in %s/%s: %w", number, owner, repo, err)
	}
	defer resp.Body.Close()

	checkRateLimit(resp)

	// 404 means no parent
	if resp.StatusCode == http.StatusNotFound {
		return nil, nil
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("failed to get parent issue for #%d in %s/%s: API error %s - %s", number, owner, repo, resp.Status, string(body))
	}

	var parent Issue
	if err := json.NewDecoder(resp.Body).Decode(&parent); err != nil {
		return nil, fmt.Errorf("failed to decode parent issue response for #%d in %s/%s: %w", number, owner, repo, err)
	}

	return &parent, nil
}

// AddSubIssue adds a sub-issue to a parent issue.
// subIssueID is the numeric ID of the issue to add as sub-issue (not the issue number).
func (c *Client) AddSubIssue(owner, repo string, parentNumber int, subIssueID int64) error {
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d/sub_issues", c.baseURL, owner, repo, parentNumber)

	payload := map[string]int64{"sub_issue_id": subIssueID}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	resp, err := c.doRequest("POST", url, bytes.NewReader(jsonPayload))
	if err != nil {
		return fmt.Errorf("failed to add sub-issue to #%d in %s/%s: %w", parentNumber, owner, repo, err)
	}
	defer resp.Body.Close()

	checkRateLimit(resp)

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to add sub-issue to #%d in %s/%s: API error %s - %s", parentNumber, owner, repo, resp.Status, string(body))
	}

	return nil
}

// RemoveSubIssue removes a sub-issue from its parent.
// subIssueID is the numeric ID of the issue to remove (not the issue number).
func (c *Client) RemoveSubIssue(owner, repo string, parentNumber int, subIssueID int64) error {
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d/sub_issue", c.baseURL, owner, repo, parentNumber)

	payload := map[string]int64{"sub_issue_id": subIssueID}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	resp, err := c.doRequest("DELETE", url, bytes.NewReader(jsonPayload))
	if err != nil {
		return fmt.Errorf("failed to remove sub-issue from #%d in %s/%s: %w", parentNumber, owner, repo, err)
	}
	defer resp.Body.Close()

	checkRateLimit(resp)

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("failed to remove sub-issue from #%d in %s/%s: API error %s - %s", parentNumber, owner, repo, resp.Status, string(body))
	}

	return nil
}
