package gh

import (
	"encoding/json"
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
	mu     sync.RWMutex
	issues map[int]*Issue // issue number -> issue
}

// NewMockServer creates a mock GitHub API server
func NewMockServer() *MockServer {
	m := &MockServer{
		issues: make(map[int]*Issue),
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

// Reset clears all issues
func (m *MockServer) Reset() {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.issues = make(map[int]*Issue)
}

func (m *MockServer) handleListIssues(w http.ResponseWriter, r *http.Request) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	issues := make([]*Issue, 0, len(m.issues))
	for _, issue := range m.issues {
		issues = append(issues, issue)
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(issues)
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
	defer m.mu.Unlock()

	issue, ok := m.issues[number]
	if !ok {
		http.Error(w, "not found", http.StatusNotFound)
		return
	}

	var update struct {
		Title string `json:"title,omitempty"`
		Body  string `json:"body,omitempty"`
	}
	if err := json.NewDecoder(r.Body).Decode(&update); err != nil {
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

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(issue)
}
