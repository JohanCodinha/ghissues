// Package sync provides the synchronization engine between local cache and GitHub.
package sync

import (
	"fmt"
	"strings"
	gosync "sync"
	"time"

	"github.com/JohanCodinha/ghissues/internal/cache"
	"github.com/JohanCodinha/ghissues/internal/gh"
	"github.com/JohanCodinha/ghissues/internal/logger"
)

// Engine handles synchronization between cache and GitHub.
type Engine struct {
	cache      *cache.DB
	client     *gh.Client
	repo       string // "owner/repo"
	owner      string
	repoName   string
	debounceMs int

	// internal state
	mu     gosync.Mutex
	timer  *time.Timer
	stopCh chan struct{}
}

// NewEngine creates a new sync engine.
// repo should be in "owner/repo" format.
// debounceMs is the debounce delay in milliseconds for write syncs.
func NewEngine(cacheDB *cache.DB, client *gh.Client, repo string, debounceMs int) (*Engine, error) {
	owner, repoName, err := parseRepo(repo)
	if err != nil {
		return nil, err
	}

	return &Engine{
		cache:      cacheDB,
		client:     client,
		repo:       repo,
		owner:      owner,
		repoName:   repoName,
		debounceMs: debounceMs,
		stopCh:     make(chan struct{}),
	}, nil
}

// parseRepo splits "owner/repo" into owner and repo name.
func parseRepo(repo string) (string, string, error) {
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid repo format %q: must be owner/repo", repo)
	}
	return parts[0], parts[1], nil
}

// InitialSync fetches all issues from GitHub and populates the cache.
// This should be called on mount.
func (e *Engine) InitialSync() error {
	logger.Debug("sync: starting initial sync for %s", e.repo)

	issues, err := e.client.ListIssues(e.owner, e.repoName)
	if err != nil {
		return fmt.Errorf("failed to list issues: %w", err)
	}

	logger.Debug("sync: fetched %d issues from GitHub", len(issues))

	for _, ghIssue := range issues {
		// Convert labels to string slice
		labels := make([]string, len(ghIssue.Labels))
		for i, l := range ghIssue.Labels {
			labels[i] = l.Name
		}

		cacheIssue := cache.Issue{
			Number:    ghIssue.Number,
			Repo:      e.repo,
			Title:     ghIssue.Title,
			Body:      ghIssue.Body,
			State:     ghIssue.State,
			Author:    ghIssue.User.Login,
			Labels:    labels,
			CreatedAt: ghIssue.CreatedAt.Format(time.RFC3339),
			UpdatedAt: ghIssue.UpdatedAt.Format(time.RFC3339),
			ETag:      ghIssue.ETag,
			Dirty:     false,
		}

		if err := e.cache.UpsertIssue(cacheIssue); err != nil {
			logger.Warn("sync: failed to upsert issue #%d: %v", ghIssue.Number, err)
			// Continue with other issues
		}

		// Fetch comments for this issue
		if err := e.syncComments(ghIssue.Number); err != nil {
			logger.Warn("sync: failed to sync comments for issue #%d: %v", ghIssue.Number, err)
			// Continue with other issues
		}
	}

	logger.Debug("sync: initial sync complete")
	return nil
}

// syncComments fetches and caches comments for an issue.
func (e *Engine) syncComments(number int) error {
	ghComments, err := e.client.ListComments(e.owner, e.repoName, number)
	if err != nil {
		return fmt.Errorf("failed to list comments: %w", err)
	}

	// Convert gh.Comment to cache.Comment
	cacheComments := make([]cache.Comment, len(ghComments))
	for i, ghComment := range ghComments {
		cacheComments[i] = cache.Comment{
			ID:          ghComment.ID,
			IssueNumber: number,
			Repo:        e.repo,
			Author:      ghComment.User.Login,
			Body:        ghComment.Body,
			CreatedAt:   ghComment.CreatedAt.Format(time.RFC3339),
			UpdatedAt:   ghComment.UpdatedAt.Format(time.RFC3339),
		}
	}

	if err := e.cache.UpsertComments(e.repo, number, cacheComments); err != nil {
		return fmt.Errorf("failed to upsert comments: %w", err)
	}

	logger.Debug("sync: synced %d comments for issue #%d", len(cacheComments), number)
	return nil
}

// RefreshIssue fetches a single issue if the etag has changed (background refresh).
// Returns true if the issue was updated in cache, false if unchanged or error.
// This uses conditional requests with If-None-Match header.
func (e *Engine) RefreshIssue(number int) (bool, error) {
	// Get the current cached issue to get its etag
	cachedIssue, err := e.cache.GetIssue(e.repo, number)
	if err != nil {
		return false, fmt.Errorf("failed to get cached issue: %w", err)
	}
	if cachedIssue == nil {
		return false, fmt.Errorf("issue #%d not found in cache", number)
	}

	// Don't refresh dirty issues - local changes take precedence
	if cachedIssue.Dirty {
		logger.Debug("sync: skipping refresh for dirty issue #%d", number)
		return false, nil
	}

	// Fetch with etag for conditional request
	ghIssue, newEtag, err := e.client.GetIssueWithEtag(e.owner, e.repoName, number, cachedIssue.ETag)
	if err != nil {
		return false, fmt.Errorf("failed to fetch issue: %w", err)
	}

	// 304 Not Modified - issue hasn't changed
	if ghIssue == nil {
		return false, nil
	}

	// Issue was updated - update cache
	labels := make([]string, len(ghIssue.Labels))
	for i, l := range ghIssue.Labels {
		labels[i] = l.Name
	}

	cacheIssue := cache.Issue{
		Number:    ghIssue.Number,
		Repo:      e.repo,
		Title:     ghIssue.Title,
		Body:      ghIssue.Body,
		State:     ghIssue.State,
		Author:    ghIssue.User.Login,
		Labels:    labels,
		CreatedAt: ghIssue.CreatedAt.Format(time.RFC3339),
		UpdatedAt: ghIssue.UpdatedAt.Format(time.RFC3339),
		ETag:      newEtag,
		Dirty:     false,
	}

	if err := e.cache.UpsertIssue(cacheIssue); err != nil {
		return false, fmt.Errorf("failed to update cache: %w", err)
	}

	// Also refresh comments for this issue
	if err := e.syncComments(number); err != nil {
		logger.Warn("sync: failed to refresh comments for issue #%d: %v", number, err)
		// Don't fail the whole refresh - issue update succeeded
	}

	logger.Debug("sync: refreshed issue #%d from GitHub", number)
	return true, nil
}

// TriggerSync schedules a debounced sync of dirty issues.
// Multiple calls within the debounce window reset the timer.
func (e *Engine) TriggerSync() {
	e.mu.Lock()
	defer e.mu.Unlock()

	// Stop existing timer if any
	if e.timer != nil {
		e.timer.Stop()
	}

	// Start new timer
	e.timer = time.AfterFunc(time.Duration(e.debounceMs)*time.Millisecond, func() {
		if err := e.syncDirtyIssues(); err != nil {
			logger.Error("sync: error syncing dirty issues: %v", err)
		}
	})

	logger.Debug("sync: debounce timer started/reset (%dms)", e.debounceMs)
}

// SyncNow immediately syncs all dirty issues, comments, and pending items.
// This should be called on unmount to ensure all changes are pushed.
func (e *Engine) SyncNow() error {
	e.mu.Lock()
	// Stop any pending timer
	if e.timer != nil {
		e.timer.Stop()
		e.timer = nil
	}
	e.mu.Unlock()

	var errs []error

	// Sync pending new comments first (so they exist before any edits)
	if err := e.syncPendingComments(); err != nil {
		errs = append(errs, fmt.Errorf("pending comments: %w", err))
	}

	// Sync dirty (edited) comments
	if err := e.syncDirtyComments(); err != nil {
		errs = append(errs, fmt.Errorf("dirty comments: %w", err))
	}

	// Sync dirty issues
	if err := e.syncDirtyIssues(); err != nil {
		errs = append(errs, fmt.Errorf("dirty issues: %w", err))
	}

	// Sync pending new issues
	if err := e.syncPendingIssues(); err != nil {
		errs = append(errs, fmt.Errorf("pending issues: %w", err))
	}

	if len(errs) > 0 {
		return fmt.Errorf("sync errors: %v", errs)
	}

	return nil
}

// syncDirtyIssues syncs all dirty issues to GitHub.
func (e *Engine) syncDirtyIssues() error {
	dirtyIssues, err := e.cache.GetDirtyIssues(e.repo)
	if err != nil {
		return fmt.Errorf("failed to get dirty issues: %w", err)
	}

	if len(dirtyIssues) == 0 {
		logger.Debug("sync: no dirty issues to sync")
		return nil
	}

	logger.Debug("sync: syncing %d dirty issues", len(dirtyIssues))

	var syncErrors []error
	for _, issue := range dirtyIssues {
		if err := e.syncIssue(issue); err != nil {
			syncErrors = append(syncErrors, fmt.Errorf("issue #%d: %w", issue.Number, err))
		}
	}

	if len(syncErrors) > 0 {
		return fmt.Errorf("sync errors: %v", syncErrors)
	}

	return nil
}

// syncIssue syncs a single dirty issue to GitHub.
// Implements conflict detection: local wins UNLESS remote was updated more recently.
func (e *Engine) syncIssue(issue cache.Issue) error {
	logger.Debug("sync: checking conflict for issue #%d", issue.Number)

	// Fetch remote to check updated_at
	remoteIssue, _, err := e.client.GetIssue(e.owner, e.repoName, issue.Number)
	if err != nil {
		return fmt.Errorf("failed to fetch remote issue: %w", err)
	}

	// Parse timestamps for conflict detection
	localUpdatedAt, err := time.Parse(time.RFC3339, issue.LocalUpdatedAt)
	if err != nil {
		return fmt.Errorf("failed to parse local_updated_at: %w", err)
	}

	remoteUpdatedAt := remoteIssue.UpdatedAt

	// Conflict detection: if remote is newer, skip push (keep dirty for user to resolve)
	if remoteUpdatedAt.After(localUpdatedAt) {
		logger.Warn("sync: conflict detected for issue #%d - remote is newer (remote: %s, local: %s), keeping dirty",
			issue.Number, remoteUpdatedAt.Format(time.RFC3339), localUpdatedAt.Format(time.RFC3339))
		// Per design: local wins UNLESS remote is newer
		// So if remote is newer, we skip push and keep the issue dirty
		return nil
	}

	// Determine which fields changed and need to be pushed
	var titlePtr, bodyPtr *string
	if issue.Title != remoteIssue.Title {
		titlePtr = &issue.Title
	}
	if issue.Body != remoteIssue.Body {
		bodyPtr = &issue.Body
	}

	// Push update to GitHub (only if something changed)
	if titlePtr != nil || bodyPtr != nil {
		logger.Debug("sync: pushing issue #%d to GitHub (title changed: %v, body changed: %v)",
			issue.Number, titlePtr != nil, bodyPtr != nil)
		if err := e.client.UpdateIssue(e.owner, e.repoName, issue.Number, titlePtr, bodyPtr); err != nil {
			return fmt.Errorf("failed to update issue on GitHub: %w", err)
		}
	} else {
		logger.Debug("sync: issue #%d marked dirty but no changes detected, clearing dirty flag", issue.Number)
	}

	// Clear dirty flag on success
	if err := e.cache.ClearDirty(e.repo, issue.Number); err != nil {
		return fmt.Errorf("failed to clear dirty flag: %w", err)
	}

	// Refresh the issue to get updated etag and updated_at
	if _, err := e.RefreshIssue(issue.Number); err != nil {
		// Log but don't fail - the sync itself succeeded
		logger.Warn("sync: failed to refresh issue #%d after sync: %v", issue.Number, err)
	}

	logger.Debug("sync: successfully synced issue #%d", issue.Number)
	return nil
}

// Stop stops the sync engine and any pending timers.
func (e *Engine) Stop() {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.timer != nil {
		e.timer.Stop()
		e.timer = nil
	}

	// Signal stop (for any future background goroutines)
	select {
	case <-e.stopCh:
		// Already closed
	default:
		close(e.stopCh)
	}

	logger.Debug("sync: engine stopped")
}

// HasConflict checks if an issue has a conflict (remote is newer than local).
// Returns true if remote updated_at > local local_updated_at.
func (e *Engine) HasConflict(number int) (bool, error) {
	cachedIssue, err := e.cache.GetIssue(e.repo, number)
	if err != nil {
		return false, fmt.Errorf("failed to get cached issue: %w", err)
	}
	if cachedIssue == nil {
		return false, fmt.Errorf("issue #%d not found in cache", number)
	}

	if !cachedIssue.Dirty {
		return false, nil // Not dirty, no conflict possible
	}

	// Fetch remote
	remoteIssue, _, err := e.client.GetIssue(e.owner, e.repoName, number)
	if err != nil {
		return false, fmt.Errorf("failed to fetch remote issue: %w", err)
	}

	localUpdatedAt, err := time.Parse(time.RFC3339, cachedIssue.LocalUpdatedAt)
	if err != nil {
		return false, fmt.Errorf("failed to parse local_updated_at: %w", err)
	}

	return remoteIssue.UpdatedAt.After(localUpdatedAt), nil
}

// syncPendingComments syncs all pending (new) comments to GitHub.
func (e *Engine) syncPendingComments() error {
	pendingComments, err := e.cache.GetPendingComments(e.repo)
	if err != nil {
		return fmt.Errorf("failed to get pending comments: %w", err)
	}

	if len(pendingComments) == 0 {
		logger.Debug("sync: no pending comments to sync")
		return nil
	}

	logger.Debug("sync: syncing %d pending comments", len(pendingComments))

	var syncErrors []error
	for _, pc := range pendingComments {
		// Create the comment on GitHub
		_, err := e.client.CreateComment(e.owner, e.repoName, pc.IssueNumber, pc.Body)
		if err != nil {
			syncErrors = append(syncErrors, fmt.Errorf("comment for issue #%d: %w", pc.IssueNumber, err))
			continue
		}

		// Remove from pending after successful sync
		if err := e.cache.RemovePendingComment(pc.ID); err != nil {
			logger.Warn("sync: failed to remove pending comment %d: %v", pc.ID, err)
		}

		// Refresh comments for this issue to get the new comment in cache
		if err := e.syncComments(pc.IssueNumber); err != nil {
			logger.Warn("sync: failed to refresh comments for issue #%d: %v", pc.IssueNumber, err)
		}

		logger.Debug("sync: created comment on issue #%d", pc.IssueNumber)
	}

	if len(syncErrors) > 0 {
		return fmt.Errorf("sync errors: %v", syncErrors)
	}

	return nil
}

// syncDirtyComments syncs all dirty (edited) comments to GitHub.
func (e *Engine) syncDirtyComments() error {
	dirtyComments, err := e.cache.GetDirtyComments(e.repo)
	if err != nil {
		return fmt.Errorf("failed to get dirty comments: %w", err)
	}

	if len(dirtyComments) == 0 {
		logger.Debug("sync: no dirty comments to sync")
		return nil
	}

	logger.Debug("sync: syncing %d dirty comments", len(dirtyComments))

	var syncErrors []error
	for _, dc := range dirtyComments {
		// Update the comment on GitHub
		err := e.client.UpdateComment(e.owner, e.repoName, dc.ID, dc.Body)
		if err != nil {
			syncErrors = append(syncErrors, fmt.Errorf("comment %d: %w", dc.ID, err))
			continue
		}

		// Clear dirty flag after successful sync
		if err := e.cache.ClearCommentDirty(e.repo, dc.ID); err != nil {
			logger.Warn("sync: failed to clear dirty flag for comment %d: %v", dc.ID, err)
		}

		logger.Debug("sync: updated comment %d on issue #%d", dc.ID, dc.IssueNumber)
	}

	if len(syncErrors) > 0 {
		return fmt.Errorf("sync errors: %v", syncErrors)
	}

	return nil
}

// syncPendingIssues syncs all pending (new) issues to GitHub.
func (e *Engine) syncPendingIssues() error {
	pendingIssues, err := e.cache.GetPendingIssues(e.repo)
	if err != nil {
		return fmt.Errorf("failed to get pending issues: %w", err)
	}

	if len(pendingIssues) == 0 {
		logger.Debug("sync: no pending issues to sync")
		return nil
	}

	logger.Debug("sync: syncing %d pending issues", len(pendingIssues))

	var syncErrors []error
	for _, pi := range pendingIssues {
		// Create the issue on GitHub
		ghIssue, err := e.client.CreateIssue(e.owner, e.repoName, pi.Title, pi.Body, pi.Labels)
		if err != nil {
			syncErrors = append(syncErrors, fmt.Errorf("issue %q: %w", pi.Title, err))
			continue
		}

		// Remove from pending after successful sync
		if err := e.cache.RemovePendingIssue(pi.ID); err != nil {
			logger.Warn("sync: failed to remove pending issue %d: %v", pi.ID, err)
		}

		// Add the newly created issue to the cache
		labels := make([]string, len(ghIssue.Labels))
		for i, l := range ghIssue.Labels {
			labels[i] = l.Name
		}

		cacheIssue := cache.Issue{
			Number:    ghIssue.Number,
			Repo:      e.repo,
			Title:     ghIssue.Title,
			Body:      ghIssue.Body,
			State:     ghIssue.State,
			Author:    ghIssue.User.Login,
			Labels:    labels,
			CreatedAt: ghIssue.CreatedAt.Format(time.RFC3339),
			UpdatedAt: ghIssue.UpdatedAt.Format(time.RFC3339),
			ETag:      ghIssue.ETag,
			Dirty:     false,
		}

		if err := e.cache.UpsertIssue(cacheIssue); err != nil {
			logger.Warn("sync: failed to cache newly created issue #%d: %v", ghIssue.Number, err)
		}

		logger.Debug("sync: created issue #%d: %s", ghIssue.Number, ghIssue.Title)
	}

	if len(syncErrors) > 0 {
		return fmt.Errorf("sync errors: %v", syncErrors)
	}

	return nil
}
