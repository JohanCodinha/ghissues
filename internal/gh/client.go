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

	"gopkg.in/yaml.v3"
)

const (
	apiBaseURL = "https://api.github.com"
)

// Label represents a GitHub issue label.
type Label struct {
	Name  string `json:"name"`
	Color string `json:"color"`
}

// User represents a GitHub user.
type User struct {
	Login string `json:"login"`
}

// Issue represents a GitHub issue.
type Issue struct {
	Number    int       `json:"number"`
	Title     string    `json:"title"`
	Body      string    `json:"body"`
	State     string    `json:"state"`
	Labels    []Label   `json:"labels"`
	User      User      `json:"user"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	ETag      string    `json:"-"` // Not from JSON, set from response header
}

// Client is a GitHub API client.
type Client struct {
	token      string
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
func (c *Client) doRequest(method, url string, body io.Reader) (*http.Response, error) {
	req, err := http.NewRequest(method, url, body)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}

	return resp, nil
}

// checkRateLimit logs rate limit information from response headers.
func checkRateLimit(resp *http.Response) {
	remaining := resp.Header.Get("X-RateLimit-Remaining")
	reset := resp.Header.Get("X-RateLimit-Reset")

	if remaining == "0" && reset != "" {
		resetTime, err := strconv.ParseInt(reset, 10, 64)
		if err == nil {
			resetAt := time.Unix(resetTime, 0)
			fmt.Fprintf(os.Stderr, "WARNING: GitHub API rate limit exceeded. Resets at %s\n", resetAt.Format(time.RFC3339))
		}
	}
}

// ListIssues fetches all open issues from the repository.
// Handles pagination automatically.
func (c *Client) ListIssues(owner, repo string) ([]Issue, error) {
	var allIssues []Issue
	url := fmt.Sprintf("%s/repos/%s/%s/issues?state=all&per_page=100", apiBaseURL, owner, repo)

	for url != "" {
		resp, err := c.doRequest("GET", url, nil)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()

		checkRateLimit(resp)

		if resp.StatusCode != http.StatusOK {
			body, _ := io.ReadAll(resp.Body)
			return nil, fmt.Errorf("GitHub API error: %s - %s", resp.Status, string(body))
		}

		var issues []Issue
		if err := json.NewDecoder(resp.Body).Decode(&issues); err != nil {
			return nil, fmt.Errorf("failed to decode response: %w", err)
		}

		allIssues = append(allIssues, issues...)

		// Parse Link header for pagination
		url = getNextPageURL(resp.Header.Get("Link"))
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
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d", apiBaseURL, owner, repo, number)

	resp, err := c.doRequest("GET", url, nil)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()

	checkRateLimit(resp)

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, "", fmt.Errorf("GitHub API error: %s - %s", resp.Status, string(body))
	}

	var issue Issue
	if err := json.NewDecoder(resp.Body).Decode(&issue); err != nil {
		return nil, "", fmt.Errorf("failed to decode response: %w", err)
	}

	etag := resp.Header.Get("ETag")
	issue.ETag = etag

	return &issue, etag, nil
}

// UpdateIssue updates an issue's body.
func (c *Client) UpdateIssue(owner, repo string, number int, body string) error {
	url := fmt.Sprintf("%s/repos/%s/%s/issues/%d", apiBaseURL, owner, repo, number)

	payload := map[string]string{"body": body}
	jsonPayload, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("failed to marshal payload: %w", err)
	}

	resp, err := c.doRequest("PATCH", url, bytes.NewReader(jsonPayload))
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	checkRateLimit(resp)

	if resp.StatusCode != http.StatusOK {
		respBody, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("GitHub API error: %s - %s", resp.Status, string(respBody))
	}

	return nil
}
