// Package sync provides the synchronization engine between local cache and GitHub.
package sync

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/JohanCodinha/ghissues/internal/cache"
	"github.com/JohanCodinha/ghissues/internal/logger"
	"github.com/JohanCodinha/ghissues/internal/md"
)

// backupConflict saves the local version of an issue to the .conflicts directory.
// This is called when remote wins a conflict, preserving the user's local changes.
// Files are saved to ~/.cache/ghissues/.conflicts/{repo}/issue_{number}_{timestamp}.md
func (e *Engine) backupConflict(issue cache.Issue) error {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}

	// Create conflict directory: ~/.cache/ghissues/.conflicts/owner_repo/
	conflictDir := filepath.Join(homeDir, ".cache", "ghissues", ".conflicts", e.repo)
	if err := os.MkdirAll(conflictDir, 0755); err != nil {
		return fmt.Errorf("failed to create conflict directory: %w", err)
	}

	// Get comments for the issue
	comments, err := e.cache.GetComments(e.repo, issue.Number)
	if err != nil {
		logger.Warn("sync: failed to get comments for conflict backup: %v", err)
		comments = []cache.Comment{} // Continue with empty comments
	}

	// Generate markdown content
	content := md.ToMarkdown(&issue, comments)

	// Write to timestamped file
	timestamp := time.Now().Format("20060102_150405")
	filename := fmt.Sprintf("issue_%d_%s.md", issue.Number, timestamp)
	filePath := filepath.Join(conflictDir, filename)

	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write conflict file: %w", err)
	}

	logger.Info("sync: backed up local changes to %s", filePath)
	return nil
}
