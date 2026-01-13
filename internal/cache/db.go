// Package cache provides SQLite-based caching for GitHub issues.
package cache

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

// DB represents a SQLite database connection for caching issues.
type DB struct {
	path string
	conn *sql.DB
}

// Issue represents a cached issue.
type Issue struct {
	ID                 int64
	Number             int
	Repo               string
	Title              string
	Body               string
	State              string
	Author             string
	Labels             []string // Stored as JSON array in database
	CreatedAt          string
	UpdatedAt          string
	ETag               string
	Dirty              bool
	LocalUpdatedAt     string
	ParentIssueNumber  int // 0 if no parent
	SubIssuesTotal     int
	SubIssuesCompleted int
}

// Comment represents a cached issue comment.
type Comment struct {
	ID          int64
	IssueNumber int
	Repo        string
	Author      string
	Body        string
	CreatedAt   string
	UpdatedAt   string
}

// createTableSQL defines the schema for the issues table.
const createTableSQL = `
CREATE TABLE IF NOT EXISTS issues (
    id INTEGER PRIMARY KEY,
    number INTEGER NOT NULL,
    repo TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT,
    state TEXT,
    author TEXT,
    labels TEXT,  -- JSON array of label names
    created_at TEXT,
    updated_at TEXT,
    etag TEXT,
    dirty INTEGER DEFAULT 0,
    local_updated_at TEXT,
    parent_issue_number INTEGER DEFAULT 0,
    sub_issues_total INTEGER DEFAULT 0,
    sub_issues_completed INTEGER DEFAULT 0,
    UNIQUE(repo, number)
);
`

// migrateSubIssuesSQL adds sub-issues columns to existing databases.
const migrateSubIssuesSQL = `
ALTER TABLE issues ADD COLUMN parent_issue_number INTEGER DEFAULT 0;
ALTER TABLE issues ADD COLUMN sub_issues_total INTEGER DEFAULT 0;
ALTER TABLE issues ADD COLUMN sub_issues_completed INTEGER DEFAULT 0;
`

// createCommentsTableSQL defines the schema for the comments table.
const createCommentsTableSQL = `
CREATE TABLE IF NOT EXISTS comments (
    id INTEGER PRIMARY KEY,
    issue_number INTEGER NOT NULL,
    repo TEXT NOT NULL,
    author TEXT NOT NULL,
    body TEXT,
    created_at TEXT,
    updated_at TEXT,
    dirty INTEGER DEFAULT 0,
    UNIQUE(repo, issue_number, id)
);
`

// createPendingCommentsTableSQL defines the schema for pending new comments.
const createPendingCommentsTableSQL = `
CREATE TABLE IF NOT EXISTS pending_comments (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    issue_number INTEGER NOT NULL,
    repo TEXT NOT NULL,
    body TEXT NOT NULL,
    created_at TEXT,
    UNIQUE(repo, issue_number, id)
);
`

// createPendingIssuesTableSQL defines the schema for pending new issues.
const createPendingIssuesTableSQL = `
CREATE TABLE IF NOT EXISTS pending_issues (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    repo TEXT NOT NULL,
    title TEXT NOT NULL,
    body TEXT,
    labels TEXT,
    created_at TEXT
);
`

// InitDB creates or opens a SQLite database at the given path and initializes the schema.
func InitDB(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	// Configure connection pool for SQLite.
	// SQLite only supports a single writer, so we limit to one connection
	// to prevent "database is locked" errors under concurrent FUSE operations.
	conn.SetMaxOpenConns(1)
	conn.SetMaxIdleConns(1)
	conn.SetConnMaxLifetime(0)

	// Create the issues table if it doesn't exist
	_, err = conn.Exec(createTableSQL)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to create issues table: %w", err)
	}

	// Create the comments table if it doesn't exist
	_, err = conn.Exec(createCommentsTableSQL)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to create comments table: %w", err)
	}

	// Create the pending_comments table if it doesn't exist
	_, err = conn.Exec(createPendingCommentsTableSQL)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to create pending_comments table: %w", err)
	}

	// Create the pending_issues table if it doesn't exist
	_, err = conn.Exec(createPendingIssuesTableSQL)
	if err != nil {
		conn.Close()
		return nil, fmt.Errorf("failed to create pending_issues table: %w", err)
	}

	// Migrate: add sub-issues columns if they don't exist
	// We run each ALTER TABLE separately and ignore errors (column may already exist)
	conn.Exec("ALTER TABLE issues ADD COLUMN parent_issue_number INTEGER DEFAULT 0")
	conn.Exec("ALTER TABLE issues ADD COLUMN sub_issues_total INTEGER DEFAULT 0")
	conn.Exec("ALTER TABLE issues ADD COLUMN sub_issues_completed INTEGER DEFAULT 0")

	return &DB{
		path: path,
		conn: conn,
	}, nil
}

// Close closes the database connection.
func (db *DB) Close() error {
	if db.conn != nil {
		return db.conn.Close()
	}
	return nil
}

// UpsertIssue inserts or updates an issue in the cache.
// Uses INSERT OR REPLACE to handle both insert and update cases.
func (db *DB) UpsertIssue(issue Issue) error {
	// Convert labels slice to JSON string
	labelsJSON, err := json.Marshal(issue.Labels)
	if err != nil {
		return fmt.Errorf("failed to marshal labels: %w", err)
	}

	// Convert dirty bool to int
	dirtyInt := 0
	if issue.Dirty {
		dirtyInt = 1
	}

	query := `
		INSERT OR REPLACE INTO issues (
			number, repo, title, body, state, author, labels,
			created_at, updated_at, etag, dirty, local_updated_at,
			parent_issue_number, sub_issues_total, sub_issues_completed
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
	`

	_, err = db.conn.Exec(query,
		issue.Number,
		issue.Repo,
		issue.Title,
		sql.NullString{String: issue.Body, Valid: issue.Body != ""},
		sql.NullString{String: issue.State, Valid: issue.State != ""},
		sql.NullString{String: issue.Author, Valid: issue.Author != ""},
		string(labelsJSON),
		sql.NullString{String: issue.CreatedAt, Valid: issue.CreatedAt != ""},
		sql.NullString{String: issue.UpdatedAt, Valid: issue.UpdatedAt != ""},
		sql.NullString{String: issue.ETag, Valid: issue.ETag != ""},
		dirtyInt,
		sql.NullString{String: issue.LocalUpdatedAt, Valid: issue.LocalUpdatedAt != ""},
		issue.ParentIssueNumber,
		issue.SubIssuesTotal,
		issue.SubIssuesCompleted,
	)
	if err != nil {
		return fmt.Errorf("failed to upsert issue: %w", err)
	}

	return nil
}

// GetIssue retrieves an issue from the cache by repo and number.
func (db *DB) GetIssue(repo string, number int) (*Issue, error) {
	query := `
		SELECT id, number, repo, title, body, state, author, labels,
		       created_at, updated_at, etag, dirty, local_updated_at,
		       parent_issue_number, sub_issues_total, sub_issues_completed
		FROM issues
		WHERE repo = ? AND number = ?
	`

	row := db.conn.QueryRow(query, repo, number)
	return scanIssueFrom(row)
}

// ListIssues retrieves all issues for a repository.
func (db *DB) ListIssues(repo string) ([]Issue, error) {
	query := `
		SELECT id, number, repo, title, body, state, author, labels,
		       created_at, updated_at, etag, dirty, local_updated_at,
		       parent_issue_number, sub_issues_total, sub_issues_completed
		FROM issues
		WHERE repo = ?
		ORDER BY number ASC
	`

	rows, err := db.conn.Query(query, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to query issues: %w", err)
	}
	defer rows.Close()

	issues := []Issue{}
	for rows.Next() {
		issue, err := scanIssueFrom(rows)
		if err != nil {
			return nil, err
		}
		issues = append(issues, *issue)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return issues, nil
}

// IssueUpdate contains optional fields for updating an issue in the cache.
// Nil fields are not updated.
type IssueUpdate struct {
	Title             *string
	Body              *string
	State             *string
	Labels            *[]string
	ParentIssueNumber *int // nil = no change, 0 = remove parent, >0 = set parent
}

// MarkDirty marks an issue as having local changes by updating the specified fields,
// setting dirty=1, and updating local_updated_at to the current time.
// Pass nil for fields you don't want to update.
func (db *DB) MarkDirty(repo string, number int, update IssueUpdate) error {
	localUpdatedAt := time.Now().UTC().Format(time.RFC3339)

	// Build dynamic query based on which fields are being updated
	var setClauses []string
	var args []interface{}

	if update.Title != nil {
		setClauses = append(setClauses, "title = ?")
		args = append(args, *update.Title)
	}
	if update.Body != nil {
		setClauses = append(setClauses, "body = ?")
		args = append(args, *update.Body)
	}
	if update.State != nil {
		setClauses = append(setClauses, "state = ?")
		args = append(args, *update.State)
	}
	if update.Labels != nil {
		labelsJSON, err := json.Marshal(*update.Labels)
		if err != nil {
			return fmt.Errorf("failed to marshal labels: %w", err)
		}
		setClauses = append(setClauses, "labels = ?")
		args = append(args, string(labelsJSON))
	}
	if update.ParentIssueNumber != nil {
		setClauses = append(setClauses, "parent_issue_number = ?")
		args = append(args, *update.ParentIssueNumber)
	}

	// Always set dirty and local_updated_at
	setClauses = append(setClauses, "dirty = 1", "local_updated_at = ?")
	args = append(args, localUpdatedAt, repo, number)

	query := fmt.Sprintf(`
		UPDATE issues
		SET %s
		WHERE repo = ? AND number = ?
	`, strings.Join(setClauses, ", "))

	result, err := db.conn.Exec(query, args...)
	if err != nil {
		return fmt.Errorf("failed to mark issue dirty: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("no issue found with repo=%s and number=%d", repo, number)
	}

	return nil
}

// GetDirtyIssues retrieves all issues with dirty=1 for a repository.
func (db *DB) GetDirtyIssues(repo string) ([]Issue, error) {
	query := `
		SELECT id, number, repo, title, body, state, author, labels,
		       created_at, updated_at, etag, dirty, local_updated_at,
		       parent_issue_number, sub_issues_total, sub_issues_completed
		FROM issues
		WHERE repo = ? AND dirty = 1
		ORDER BY number ASC
	`

	rows, err := db.conn.Query(query, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to query dirty issues: %w", err)
	}
	defer rows.Close()

	issues := []Issue{}
	for rows.Next() {
		issue, err := scanIssueFrom(rows)
		if err != nil {
			return nil, err
		}
		issues = append(issues, *issue)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating rows: %w", err)
	}

	return issues, nil
}

// ClearDirty clears the dirty flag for an issue after successful sync.
func (db *DB) ClearDirty(repo string, number int) error {
	query := `
		UPDATE issues
		SET dirty = 0
		WHERE repo = ? AND number = ?
	`

	result, err := db.conn.Exec(query, repo, number)
	if err != nil {
		return fmt.Errorf("failed to clear dirty flag: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("no issue found with repo=%s and number=%d", repo, number)
	}

	return nil
}

// scanner is an interface that both *sql.Row and *sql.Rows implement.
type scanner interface {
	Scan(dest ...interface{}) error
}

// scanIssueFrom scans a row into an Issue struct using the scanner interface.
// This handles both *sql.Row and *sql.Rows.
func scanIssueFrom(s scanner) (*Issue, error) {
	var issue Issue
	var body, state, author, labels, createdAt, updatedAt, etag, localUpdatedAt sql.NullString
	var dirty int
	var parentIssueNumber, subIssuesTotal, subIssuesCompleted sql.NullInt64

	err := s.Scan(
		&issue.ID,
		&issue.Number,
		&issue.Repo,
		&issue.Title,
		&body,
		&state,
		&author,
		&labels,
		&createdAt,
		&updatedAt,
		&etag,
		&dirty,
		&localUpdatedAt,
		&parentIssueNumber,
		&subIssuesTotal,
		&subIssuesCompleted,
	)
	if err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("failed to scan issue: %w", err)
	}

	// Handle nullable fields
	issue.Body = body.String
	issue.State = state.String
	issue.Author = author.String
	issue.CreatedAt = createdAt.String
	issue.UpdatedAt = updatedAt.String
	issue.ETag = etag.String
	issue.LocalUpdatedAt = localUpdatedAt.String
	issue.Dirty = dirty == 1
	issue.ParentIssueNumber = int(parentIssueNumber.Int64)
	issue.SubIssuesTotal = int(subIssuesTotal.Int64)
	issue.SubIssuesCompleted = int(subIssuesCompleted.Int64)

	// Parse labels JSON
	if labels.Valid && labels.String != "" {
		if err := json.Unmarshal([]byte(labels.String), &issue.Labels); err != nil {
			return nil, fmt.Errorf("failed to unmarshal labels: %w", err)
		}
	}

	return &issue, nil
}

// UpsertComments inserts or updates comments for an issue in the cache.
// This replaces all existing comments for the issue with the provided comments.
func (db *DB) UpsertComments(repo string, issueNumber int, comments []Comment) error {
	// Start a transaction to ensure atomicity
	tx, err := db.conn.Begin()
	if err != nil {
		return fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer tx.Rollback()

	// Delete existing comments for this issue
	_, err = tx.Exec("DELETE FROM comments WHERE repo = ? AND issue_number = ?", repo, issueNumber)
	if err != nil {
		return fmt.Errorf("failed to delete existing comments: %w", err)
	}

	// Insert new comments
	query := `
		INSERT INTO comments (id, issue_number, repo, author, body, created_at, updated_at)
		VALUES (?, ?, ?, ?, ?, ?, ?)
	`

	for _, comment := range comments {
		_, err = tx.Exec(query,
			comment.ID,
			issueNumber,
			repo,
			comment.Author,
			sql.NullString{String: comment.Body, Valid: comment.Body != ""},
			sql.NullString{String: comment.CreatedAt, Valid: comment.CreatedAt != ""},
			sql.NullString{String: comment.UpdatedAt, Valid: comment.UpdatedAt != ""},
		)
		if err != nil {
			return fmt.Errorf("failed to insert comment %d: %w", comment.ID, err)
		}
	}

	if err := tx.Commit(); err != nil {
		return fmt.Errorf("failed to commit transaction: %w", err)
	}

	return nil
}

// GetComments retrieves all comments for an issue from the cache.
// Comments are ordered by created_at ascending.
func (db *DB) GetComments(repo string, issueNumber int) ([]Comment, error) {
	query := `
		SELECT id, issue_number, repo, author, body, created_at, updated_at
		FROM comments
		WHERE repo = ? AND issue_number = ?
		ORDER BY created_at ASC
	`

	rows, err := db.conn.Query(query, repo, issueNumber)
	if err != nil {
		return nil, fmt.Errorf("failed to query comments: %w", err)
	}
	defer rows.Close()

	comments := []Comment{}
	for rows.Next() {
		var comment Comment
		var body, createdAt, updatedAt sql.NullString

		err := rows.Scan(
			&comment.ID,
			&comment.IssueNumber,
			&comment.Repo,
			&comment.Author,
			&body,
			&createdAt,
			&updatedAt,
		)
		if err != nil {
			return nil, fmt.Errorf("failed to scan comment: %w", err)
		}

		comment.Body = body.String
		comment.CreatedAt = createdAt.String
		comment.UpdatedAt = updatedAt.String

		comments = append(comments, comment)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating comment rows: %w", err)
	}

	return comments, nil
}

// PendingComment represents a new comment waiting to be synced.
type PendingComment struct {
	ID          int64
	IssueNumber int
	Repo        string
	Body        string
	CreatedAt   string
}

// PendingIssue represents a new issue waiting to be synced.
type PendingIssue struct {
	ID        int64
	Repo      string
	Title     string
	Body      string
	Labels    []string
	CreatedAt string
}

// AddPendingComment adds a new pending comment to be synced to GitHub.
func (db *DB) AddPendingComment(repo string, issueNumber int, body string) error {
	createdAt := time.Now().UTC().Format(time.RFC3339)

	query := `
		INSERT INTO pending_comments (issue_number, repo, body, created_at)
		VALUES (?, ?, ?, ?)
	`

	_, err := db.conn.Exec(query, issueNumber, repo, body, createdAt)
	if err != nil {
		return fmt.Errorf("failed to add pending comment: %w", err)
	}

	return nil
}

// GetPendingComments retrieves all pending comments for a repository.
func (db *DB) GetPendingComments(repo string) ([]PendingComment, error) {
	query := `
		SELECT id, issue_number, repo, body, created_at
		FROM pending_comments
		WHERE repo = ?
		ORDER BY created_at ASC
	`

	rows, err := db.conn.Query(query, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to query pending comments: %w", err)
	}
	defer rows.Close()

	var comments []PendingComment
	for rows.Next() {
		var c PendingComment
		var createdAt sql.NullString

		err := rows.Scan(&c.ID, &c.IssueNumber, &c.Repo, &c.Body, &createdAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan pending comment: %w", err)
		}
		c.CreatedAt = createdAt.String
		comments = append(comments, c)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating pending comment rows: %w", err)
	}

	return comments, nil
}

// RemovePendingComment removes a pending comment after successful sync.
func (db *DB) RemovePendingComment(id int64) error {
	_, err := db.conn.Exec("DELETE FROM pending_comments WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to remove pending comment: %w", err)
	}
	return nil
}

// MarkCommentDirty marks an existing comment as dirty (edited locally).
func (db *DB) MarkCommentDirty(repo string, commentID int64, newBody string) error {
	query := `
		UPDATE comments
		SET body = ?, dirty = 1
		WHERE repo = ? AND id = ?
	`

	result, err := db.conn.Exec(query, newBody, repo, commentID)
	if err != nil {
		return fmt.Errorf("failed to mark comment dirty: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to get rows affected: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("no comment found with repo=%s and id=%d", repo, commentID)
	}

	return nil
}

// DirtyComment represents an edited comment to be synced.
type DirtyComment struct {
	ID          int64
	IssueNumber int
	Repo        string
	Body        string
}

// GetDirtyComments retrieves all comments with dirty=1 for a repository.
func (db *DB) GetDirtyComments(repo string) ([]DirtyComment, error) {
	query := `
		SELECT id, issue_number, repo, body
		FROM comments
		WHERE repo = ? AND dirty = 1
		ORDER BY id ASC
	`

	rows, err := db.conn.Query(query, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to query dirty comments: %w", err)
	}
	defer rows.Close()

	var comments []DirtyComment
	for rows.Next() {
		var c DirtyComment
		err := rows.Scan(&c.ID, &c.IssueNumber, &c.Repo, &c.Body)
		if err != nil {
			return nil, fmt.Errorf("failed to scan dirty comment: %w", err)
		}
		comments = append(comments, c)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating dirty comment rows: %w", err)
	}

	return comments, nil
}

// ClearCommentDirty clears the dirty flag for a comment after successful sync.
func (db *DB) ClearCommentDirty(repo string, commentID int64) error {
	query := `
		UPDATE comments
		SET dirty = 0
		WHERE repo = ? AND id = ?
	`

	_, err := db.conn.Exec(query, repo, commentID)
	if err != nil {
		return fmt.Errorf("failed to clear comment dirty flag: %w", err)
	}

	return nil
}

// AddPendingIssue adds a new pending issue to be synced to GitHub.
func (db *DB) AddPendingIssue(repo, title, body string, labels []string) (int64, error) {
	createdAt := time.Now().UTC().Format(time.RFC3339)

	labelsJSON, err := json.Marshal(labels)
	if err != nil {
		return 0, fmt.Errorf("failed to marshal labels: %w", err)
	}

	query := `
		INSERT INTO pending_issues (repo, title, body, labels, created_at)
		VALUES (?, ?, ?, ?, ?)
	`

	result, err := db.conn.Exec(query, repo, title, body, string(labelsJSON), createdAt)
	if err != nil {
		return 0, fmt.Errorf("failed to add pending issue: %w", err)
	}

	id, err := result.LastInsertId()
	if err != nil {
		return 0, fmt.Errorf("failed to get last insert id: %w", err)
	}

	return id, nil
}

// GetPendingIssues retrieves all pending issues for a repository.
func (db *DB) GetPendingIssues(repo string) ([]PendingIssue, error) {
	query := `
		SELECT id, repo, title, body, labels, created_at
		FROM pending_issues
		WHERE repo = ?
		ORDER BY created_at ASC
	`

	rows, err := db.conn.Query(query, repo)
	if err != nil {
		return nil, fmt.Errorf("failed to query pending issues: %w", err)
	}
	defer rows.Close()

	var issues []PendingIssue
	for rows.Next() {
		var i PendingIssue
		var body, labels, createdAt sql.NullString

		err := rows.Scan(&i.ID, &i.Repo, &i.Title, &body, &labels, &createdAt)
		if err != nil {
			return nil, fmt.Errorf("failed to scan pending issue: %w", err)
		}

		i.Body = body.String
		i.CreatedAt = createdAt.String

		// Parse labels JSON
		if labels.Valid && labels.String != "" {
			if err := json.Unmarshal([]byte(labels.String), &i.Labels); err != nil {
				return nil, fmt.Errorf("failed to unmarshal labels: %w", err)
			}
		}

		issues = append(issues, i)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating pending issue rows: %w", err)
	}

	return issues, nil
}

// RemovePendingIssue removes a pending issue after successful sync.
func (db *DB) RemovePendingIssue(id int64) error {
	_, err := db.conn.Exec("DELETE FROM pending_issues WHERE id = ?", id)
	if err != nil {
		return fmt.Errorf("failed to remove pending issue: %w", err)
	}
	return nil
}
