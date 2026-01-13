// Package cache provides SQLite-based caching for GitHub issues.
package cache

import (
	"database/sql"
	"encoding/json"
	"fmt"
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
	ID             int64
	Number         int
	Repo           string
	Title          string
	Body           string
	State          string
	Author         string
	Labels         []string // Stored as JSON array in database
	CreatedAt      string
	UpdatedAt      string
	ETag           string
	Dirty          bool
	LocalUpdatedAt string
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
    UNIQUE(repo, number)
);
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
    UNIQUE(repo, issue_number, id)
);
`

// InitDB creates or opens a SQLite database at the given path and initializes the schema.
func InitDB(path string) (*DB, error) {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

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
			created_at, updated_at, etag, dirty, local_updated_at
		) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
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
		       created_at, updated_at, etag, dirty, local_updated_at
		FROM issues
		WHERE repo = ? AND number = ?
	`

	row := db.conn.QueryRow(query, repo, number)
	return scanIssue(row)
}

// ListIssues retrieves all issues for a repository.
func (db *DB) ListIssues(repo string) ([]Issue, error) {
	query := `
		SELECT id, number, repo, title, body, state, author, labels,
		       created_at, updated_at, etag, dirty, local_updated_at
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
		issue, err := scanIssueFromRows(rows)
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

// MarkDirty marks an issue as having local changes by updating the body,
// setting dirty=1, and updating local_updated_at to the current time.
func (db *DB) MarkDirty(repo string, number int, newBody string) error {
	localUpdatedAt := time.Now().UTC().Format(time.RFC3339)

	query := `
		UPDATE issues
		SET body = ?, dirty = 1, local_updated_at = ?
		WHERE repo = ? AND number = ?
	`

	result, err := db.conn.Exec(query, newBody, localUpdatedAt, repo, number)
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
		       created_at, updated_at, etag, dirty, local_updated_at
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
		issue, err := scanIssueFromRows(rows)
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

// scanIssue scans a single row into an Issue struct.
// Used with QueryRow which returns *sql.Row.
func scanIssue(row *sql.Row) (*Issue, error) {
	var issue Issue
	var body, state, author, labels, createdAt, updatedAt, etag, localUpdatedAt sql.NullString
	var dirty int

	err := row.Scan(
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

	// Parse labels JSON
	if labels.Valid && labels.String != "" {
		if err := json.Unmarshal([]byte(labels.String), &issue.Labels); err != nil {
			return nil, fmt.Errorf("failed to unmarshal labels: %w", err)
		}
	}

	return &issue, nil
}

// scanIssueFromRows scans a row from sql.Rows into an Issue struct.
// Used with Query which returns *sql.Rows.
func scanIssueFromRows(rows *sql.Rows) (*Issue, error) {
	var issue Issue
	var body, state, author, labels, createdAt, updatedAt, etag, localUpdatedAt sql.NullString
	var dirty int

	err := rows.Scan(
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
	)
	if err != nil {
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
