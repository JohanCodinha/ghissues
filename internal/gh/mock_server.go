package gh

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"time"
)

// MockServer provides a fake GitHub API for testing
type MockServer struct {
	*httptest.Server
	mu       sync.RWMutex
	issues   map[int]*Issue              // issue number -> issue
	comments map[int][]*Comment          // issue number -> comments

	// Pagination settings
	issuesPerPage   int // 0 means return all in one page
	commentsPerPage int // 0 means return all in one page

	// Error simulation
	forceStatusCode int    // If set, return this status code for next request
	forceErrorBody  string // Error body to return with forceStatusCode
}

// NewMockServer creates a mock GitHub API server
func NewMockServer() *MockServer {
	m := &MockServer{
		issues:   make(map[int]*Issue),
		comments: make(map[int][]*Comment),
	}

	mux := http.NewServeMux()

	// List issues: GET /repos/{owner}/{repo}/issues
	mux.HandleFunc("/repos/", func(w http.ResponseWriter, r *http.Request) {
		parts := strings.Split(strings.TrimPrefix(r.URL.Path, "/repos/"), "/")
		if len(parts) < 3 {
			http.Error(w, "invalid path", http.StatusBadRequest)
			return
		}

		// /repos/{owner}/{repo}/issues
		if parts[2] == "issues" {
			if len(parts) == 3 {
				// List issues
				if r.Method == http.MethodGet {
					m.handleListIssues(w, r)
					return
				}
			} else if len(parts) == 4 {
				// Single issue: /repos/{owner}/{repo}/issues/{number}
				number, err := strconv.Atoi(parts[3])
				if err != nil {
					http.Error(w, "invalid issue number", http.StatusBadRequest)
					return
				}
				switch r.Method {
				case http.MethodGet:
					m.handleGetIssue(w, r, number)
				case http.MethodPatch:
					m.handleUpdateIssue(w, r, number)
				default:
					http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
				}
				return
			} else if len(parts) == 5 && parts[4] == "comments" {
				// List comments: /repos/{owner}/{repo}/issues/{number}/comments
				number, err := strconv.Atoi(parts[3])
				if err != nil {
					http.Error(w, "invalid issue number", http.StatusBadRequest)
					return
				}
				if r.Method == http.MethodGet {
					m.handleListComments(w, r, number)
					return
				}
			}
		}
		http.Error(w, "not found", http.StatusNotFound)
	})

	m.Server = httptest.NewServer(mux)
	return m
}

// AddIssue adds an issue to the mock server
func (m *MockServer) AddIssue(issue *Issue) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.issues[issue.Number] = issue
}

// GetIssue retrieves an issue (for test assertions)
func (m *MockServer) GetIssue(number int) *Issue {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.issues[number]
}

// Reset clears all issues and comments
func (m *MockServer) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.issues = make(map[int]*Issue)
	m.comments = make(map[int][]*Comment)
}

// AddComment adds a comment to an issue in the mock server
func (m *MockServer) AddComment(issueNumber int, comment *Comment) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.comments[issueNumber] = append(m.comments[issueNumber], comment)
}

// GetComments retrieves comments for an issue (for test assertions)
func (m *MockServer) GetComments(issueNumber int) []*Comment {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.comments[issueNumber]
}

// SetIssuesPerPage sets pagination for issues (0 = no pagination)
func (m *MockServer) SetIssuesPerPage(perPage int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.issuesPerPage = perPage
}

// SetCommentsPerPage sets pagination for comments (0 = no pagination)
func (m *MockServer) SetCommentsPerPage(perPage int) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.commentsPerPage = perPage
}

// SetNextError sets an error to be returned for the next request
func (m *MockServer) SetNextError(statusCode int, body string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.forceStatusCode = statusCode
	m.forceErrorBody = body
}

// clearError clears any forced error (internal use)
func (m *MockServer) clearError() (int, string) {
	code := m.forceStatusCode
	body := m.forceErrorBody
	m.forceStatusCode = 0
	m.forceErrorBody = ""
	return code, body
}

func (m *MockServer) handleListIssues(w http.ResponseWriter, r *http.Request) {
	m.mu.Lock()
	// Check for forced error
	if code, body := m.clearError(); code != 0 {
		m.mu.Unlock()
		http.Error(w, body, code)
		return
	}

	issues := make([]*Issue, 0, len(m.issues))
	for _, issue := range m.issues {
		issues = append(issues, issue)
	}
	perPage := m.issuesPerPage
	m.mu.Unlock()

	// Sort issues by number for consistent pagination
	sortIssuesByNumber(issues)

	// Handle pagination
	if perPage > 0 {
		page := 1
		if p := r.URL.Query().Get("page"); p != "" {
			page, _ = strconv.Atoi(p)
			if page < 1 {
				page = 1
			}
		}

		start := (page - 1) * perPage
		end := start + perPage

		if start >= len(issues) {
			issues = []*Issue{}
		} else {
			if end > len(issues) {
				end = len(issues)
			}
			issues = issues[start:end]

			// Add Link header for next page if there are more
			totalPages := (len(m.issues) + perPage - 1) / perPage
			if page < totalPages {
				nextURL := fmt.Sprintf("%s%s?page=%d", m.Server.URL, r.URL.Path, page+1)
				w.Header().Set("Link", fmt.Sprintf(`<%s>; rel="next"`, nextURL))
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(issues)
}

// sortIssuesByNumber sorts issues by their number (ascending)
func sortIssuesByNumber(issues []*Issue) {
	for i := 0; i < len(issues)-1; i++ {
		for j := i + 1; j < len(issues); j++ {
			if issues[i].Number > issues[j].Number {
				issues[i], issues[j] = issues[j], issues[i]
			}
		}
	}
}

func (m *MockServer) handleGetIssue(w http.ResponseWriter, r *http.Request, number int) {
	m.mu.RLock()
	issue, ok := m.issues[number]
	m.mu.RUnlock()

	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	// Handle conditional request (If-None-Match)
	if etag := r.Header.Get("If-None-Match"); etag != "" && etag == issue.ETag {
		w.WriteHeader(http.StatusNotModified)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("ETag", issue.ETag)
	json.NewEncoder(w).Encode(issue)
}

func (m *MockServer) handleUpdateIssue(w http.ResponseWriter, r *http.Request, number int) {
	m.mu.Lock()
	// Check for forced error first
	if code, body := m.clearError(); code != 0 {
		m.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(code)
		w.Write([]byte(body))
		return
	}

	issue, ok := m.issues[number]
	if !ok {
		m.mu.Unlock()
		http.Error(w, `{"message":"Not Found"}`, http.StatusNotFound)
		return
	}

	var update struct {
		Title string `json:"title,omitempty"`
		Body  string `json:"body,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
		m.mu.Unlock()
		http.Error(w, "invalid json", http.StatusBadRequest)
		return
	}

	if update.Title != "" {
		issue.Title = update.Title
	}
	if update.Body != "" {
		issue.Body = update.Body
	}
	issue.UpdatedAt = time.Now().UTC()
	issue.ETag = `"` + strconv.FormatInt(time.Now().UnixNano(), 16) + `"`
	m.mu.Unlock()

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(issue)
}

func (m *MockServer) handleListComments(w http.ResponseWriter, r *http.Request, number int) {
	m.mu.Lock()
	// Check for forced error
	if code, body := m.clearError(); code != 0 {
		m.mu.Unlock()
		http.Error(w, body, code)
		return
	}

	comments := m.comments[number]
	if comments == nil {
		comments = []*Comment{}
	}
	perPage := m.commentsPerPage
	totalComments := len(comments)
	m.mu.Unlock()

	// Handle pagination
	if perPage > 0 && len(comments) > 0 {
		page := 1
		if p := r.URL.Query().Get("page"); p != "" {
			page, _ = strconv.Atoi(p)
			if page < 1 {
				page = 1
			}
		}

		start := (page - 1) * perPage
		end := start + perPage

		if start >= len(comments) {
			comments = []*Comment{}
		} else {
			if end > len(comments) {
				end = len(comments)
			}
			comments = comments[start:end]

			// Add Link header for next page if there are more
			totalPages := (totalComments + perPage - 1) / perPage
			if page < totalPages {
				nextURL := fmt.Sprintf("%s%s?page=%d", m.Server.URL, r.URL.Path, page+1)
				w.Header().Set("Link", fmt.Sprintf(`<%s>; rel="next"`, nextURL))
			}
		}
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(comments)
}
