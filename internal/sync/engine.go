// Package sync provides the synchronization engine between local cache and GitHub.
package sync

import (
	"fmt"
	"strings"
	gosync "sync"
	"time"

	"github.com/JohanCodinha/ghissues/internal/cache"
	"github.com/JohanCodinha/ghissues/internal/fs"
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

	// status tracking
	lastSyncTime time.Time
	lastError    error

	// background refresh state
	refreshTimes map[int]time.Time // last refresh time per issue
	refreshing   map[int]bool      // in-flight refresh tracking
	refreshMu    gosync.Mutex      // protects refresh state
	refreshTTL   time.Duration     // TTL before allowing re-refresh (default 30s)
}

// GetStatus returns the current sync status.
// Implements fs.StatusProvider interface.
func (e *Engine) GetStatus() fs.SyncStatus {
	e.mu.Lock()
	defer e.mu.Unlock()

	status := fs.SyncStatus{
		LastSyncTime: e.lastSyncTime,
	}
	if e.lastError != nil {
		status.LastError = e.lastError.Error()
	}

	// Get counts from cache
	if pendingIssues, err := e.cache.GetPendingIssues(e.repo); err == nil {
		status.PendingIssues = len(pendingIssues)
	}
	if pendingComments, err := e.cache.GetPendingComments(e.repo); err == nil {
		status.PendingComments = len(pendingComments)
	}
	if dirtyIssues, err := e.cache.GetDirtyIssues(e.repo); err == nil {
		status.DirtyIssues = len(dirtyIssues)
	}
	if dirtyComments, err := e.cache.GetDirtyComments(e.repo); err == nil {
		status.DirtyComments = len(dirtyComments)
	}

	return status
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
		cache:        cacheDB,
		client:       client,
		repo:         repo,
		owner:        owner,
		repoName:     repoName,
		debounceMs:   debounceMs,
		stopCh:       make(chan struct{}),
		refreshTimes: make(map[int]time.Time),
		refreshing:   make(map[int]bool),
		refreshTTL:   30 * time.Second,
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

// ghIssueToCacheIssue converts a GitHub issue to a cache issue.
func (e *Engine) ghIssueToCacheIssue(ghIssue *gh.Issue) cache.Issue {
	labels := make([]string, len(ghIssue.Labels))
	for i, l := range ghIssue.Labels {
		labels[i] = l.Name
	}

	// Extract parent issue number from URL if present
	parentIssueNumber := 0
	if ghIssue.ParentIssueURL != "" {
		parentIssueNumber = parseIssueNumberFromURL(ghIssue.ParentIssueURL)
	}

	// Extract sub-issues summary
	subIssuesTotal := 0
	subIssuesCompleted := 0
	if ghIssue.SubIssuesSummary != nil {
		subIssuesTotal = ghIssue.SubIssuesSummary.Total
		subIssuesCompleted = ghIssue.SubIssuesSummary.Completed
	}

	return cache.Issue{
		Number:             ghIssue.Number,
		ID:                 ghIssue.ID,
		Repo:               e.repo,
		Title:              ghIssue.Title,
		Body:               ghIssue.Body,
		State:              ghIssue.State,
		Author:             ghIssue.User.Login,
		Labels:             labels,
		CreatedAt:          ghIssue.CreatedAt.Format(time.RFC3339),
		UpdatedAt:          ghIssue.UpdatedAt.Format(time.RFC3339),
		ETag:               ghIssue.ETag,
		Dirty:              false,
		ParentIssueNumber:  parentIssueNumber,
		SubIssuesTotal:     subIssuesTotal,
		SubIssuesCompleted: subIssuesCompleted,
	}
}

// parseIssueNumberFromURL extracts the issue number from a GitHub API URL.
// Example: https://api.github.com/repos/owner/repo/issues/4 -> 4
func parseIssueNumberFromURL(url string) int {
	parts := strings.Split(url, "/")
	if len(parts) == 0 {
		return 0
	}
	lastPart := parts[len(parts)-1]
	var number int
	fmt.Sscanf(lastPart, "%d", &number)
	return number
}

// syncParentIssue syncs the parent-child relationship for an issue.
// It adds or removes the sub-issue relationship based on the local vs remote state.
func (e *Engine) syncParentIssue(issue cache.Issue, remoteParentNumber int) error {
	// Get the issue's numeric ID (needed for sub-issue API)
	// We need to fetch the full issue to get its ID
	ghIssue, _, err := e.client.GetIssue(e.owner, e.repoName, issue.Number)
	if err != nil {
		return fmt.Errorf("failed to get issue ID: %w", err)
	}
	issueID := ghIssue.ID

	// If removing parent (was set, now 0)
	if issue.ParentIssueNumber == 0 && remoteParentNumber > 0 {
		logger.Debug("sync: removing issue #%d from parent #%d", issue.Number, remoteParentNumber)
		if err := e.client.RemoveSubIssue(e.owner, e.repoName, remoteParentNumber, issueID); err != nil {
			return fmt.Errorf("failed to remove sub-issue: %w", err)
		}
		return nil
	}

	// If setting new parent (was 0 or different, now set)
	if issue.ParentIssueNumber > 0 {
		// If there was an old parent, remove from it first
		if remoteParentNumber > 0 && remoteParentNumber != issue.ParentIssueNumber {
			logger.Debug("sync: removing issue #%d from old parent #%d", issue.Number, remoteParentNumber)
			if err := e.client.RemoveSubIssue(e.owner, e.repoName, remoteParentNumber, issueID); err != nil {
				logger.Warn("sync: failed to remove from old parent: %v", err)
				// Continue to try adding to new parent
			}
		}

		// Add to new parent
		logger.Debug("sync: adding issue #%d as sub-issue of #%d", issue.Number, issue.ParentIssueNumber)
		if err := e.client.AddSubIssue(e.owner, e.repoName, issue.ParentIssueNumber, issueID); err != nil {
			return fmt.Errorf("failed to add sub-issue: %w", err)
		}
	}

	return nil
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
		cacheIssue := e.ghIssueToCacheIssue(&ghIssue)
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
	ghIssue, _, err := e.client.GetIssueWithEtag(e.owner, e.repoName, number, cachedIssue.ETag)
	if err != nil {
		return false, fmt.Errorf("failed to fetch issue: %w", err)
	}

	// 304 Not Modified - issue hasn't changed
	if ghIssue == nil {
		return false, nil
	}

	// Issue was updated - update cache
	// Note: ghIssue.ETag is set by GetIssueWithEtag, newEtag is the same value
	cacheIssue := e.ghIssueToCacheIssue(ghIssue)
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

// TriggerRefresh schedules a background refresh for an issue if:
// 1. The issue hasn't been refreshed within the TTL window
// 2. A refresh isn't already in flight for this issue
// This method returns immediately and doesn't block the caller.
func (e *Engine) TriggerRefresh(number int) {
	e.refreshMu.Lock()

	// Check TTL - skip if recently refreshed
	if lastRefresh, ok := e.refreshTimes[number]; ok {
		if time.Since(lastRefresh) < e.refreshTTL {
			e.refreshMu.Unlock()
			return
		}
	}

	// Check in-flight - skip if already refreshing
	if e.refreshing[number] {
		e.refreshMu.Unlock()
		return
	}

	// Mark as refreshing and update time immediately
	// (prevents rapid duplicate triggers)
	e.refreshing[number] = true
	e.refreshTimes[number] = time.Now()
	e.refreshMu.Unlock()

	// Spawn background goroutine
	go func() {
		defer func() {
			e.refreshMu.Lock()
			delete(e.refreshing, number)
			e.refreshMu.Unlock()
		}()

		// Check stopCh before making API call
		select {
		case <-e.stopCh:
			return
		default:
		}

		updated, err := e.RefreshIssue(number)
		if err != nil {
			logger.Debug("sync: background refresh failed for #%d: %v", number, err)
		} else if updated {
			logger.Debug("sync: background refresh updated #%d", number)
		}
	}()
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
		// Sync pending new issues first (so they get issue numbers before comments are added)
		if err := e.syncPendingIssues(); err != nil {
			logger.Error("sync: error syncing pending issues: %v", err)
		}
		// Sync pending new comments (can now reference correct issue numbers)
		if err := e.syncPendingComments(); err != nil {
			logger.Error("sync: error syncing pending comments: %v", err)
		}
		// Sync dirty (edited) comments
		if err := e.syncDirtyComments(); err != nil {
			logger.Error("sync: error syncing dirty comments: %v", err)
		}
		// Sync dirty issues
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

	// Sync pending new issues first (so they get issue numbers before comments are added)
	if err := e.syncPendingIssues(); err != nil {
		errs = append(errs, fmt.Errorf("pending issues: %w", err))
	}

	// Sync pending new comments (can now reference correct issue numbers)
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

	// Update status tracking
	e.mu.Lock()
	e.lastSyncTime = time.Now()
	if len(errs) > 0 {
		// Join multiple errors into a single error with context
		errMsgs := make([]string, len(errs))
		for i, err := range errs {
			errMsgs[i] = err.Error()
		}
		e.lastError = fmt.Errorf("sync errors: %s", strings.Join(errMsgs, "; "))
		e.mu.Unlock()
		return e.lastError
	}
	e.lastError = nil
	e.mu.Unlock()

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
		errMsgs := make([]string, len(syncErrors))
		for i, e := range syncErrors {
			errMsgs[i] = e.Error()
		}
		return fmt.Errorf("failed to sync %d dirty issues: %s", len(syncErrors), strings.Join(errMsgs, "; "))
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

	// Conflict detection: if remote is newer, remote wins (backup local first)
	if remoteUpdatedAt.After(localUpdatedAt) {
		logger.Warn("sync: conflict detected for issue #%d - remote is newer (remote: %s, local: %s), applying remote",
			issue.Number, remoteUpdatedAt.Format(time.RFC3339), localUpdatedAt.Format(time.RFC3339))

		// 1. Backup local version to .conflicts/
		if err := e.backupConflict(issue); err != nil {
			logger.Warn("sync: failed to backup conflict: %v", err)
			// Continue anyway - don't fail the sync just because backup failed
		}

		// 2. Fetch comments from remote
		ghComments, err := e.client.ListComments(e.owner, e.repoName, issue.Number)
		if err != nil {
			logger.Warn("sync: failed to fetch remote comments for conflict resolution: %v", err)
			// Continue with issue update only
		}

		// 3. Overwrite cache with remote version
		cacheIssue := e.ghIssueToCacheIssue(remoteIssue)
		if err := e.cache.UpsertIssue(cacheIssue); err != nil {
			return fmt.Errorf("failed to apply remote issue: %w", err)
		}

		// Update comments if we got them
		if ghComments != nil {
			cacheComments := make([]cache.Comment, len(ghComments))
			for i, c := range ghComments {
				cacheComments[i] = cache.Comment{
					ID:          c.ID,
					IssueNumber: issue.Number,
					Repo:        e.repo,
					Author:      c.User.Login,
					Body:        c.Body,
					CreatedAt:   c.CreatedAt.Format(time.RFC3339),
					UpdatedAt:   c.UpdatedAt.Format(time.RFC3339),
				}
			}
			if err := e.cache.UpsertComments(e.repo, issue.Number, cacheComments); err != nil {
				logger.Warn("sync: failed to update comments from remote: %v", err)
			}
		}

		// 4. Clear dirty flag since conflict is resolved
		if err := e.cache.ClearDirty(e.repo, issue.Number); err != nil {
			logger.Warn("sync: failed to clear dirty flag: %v", err)
		}

		return nil
	}

	// Build update struct with only changed fields
	update := gh.IssueUpdate{}
	hasChanges := false

	if issue.Title != remoteIssue.Title {
		update.Title = &issue.Title
		hasChanges = true
	}
	if issue.Body != remoteIssue.Body {
		update.Body = &issue.Body
		hasChanges = true
	}
	if issue.State != remoteIssue.State {
		update.State = &issue.State
		hasChanges = true
	}

	// Compare labels (convert remote labels to string slice)
	remoteLabels := make([]string, len(remoteIssue.Labels))
	for i, l := range remoteIssue.Labels {
		remoteLabels[i] = l.Name
	}
	if !labelsEqual(issue.Labels, remoteLabels) {
		update.Labels = &issue.Labels
		hasChanges = true
	}

	// Push update to GitHub (only if something changed)
	if hasChanges {
		logger.Debug("sync: pushing issue #%d to GitHub (title: %v, body: %v, state: %v, labels: %v)",
			issue.Number, update.Title != nil, update.Body != nil, update.State != nil, update.Labels != nil)
		if err := e.client.UpdateIssue(e.owner, e.repoName, issue.Number, update); err != nil {
			return fmt.Errorf("failed to update issue on GitHub: %w", err)
		}
	} else {
		logger.Debug("sync: issue #%d marked dirty but no changes detected, clearing dirty flag", issue.Number)
	}

	// Handle parent issue changes (sub-issue relationships are managed via separate API)
	remoteParentNumber := parseIssueNumberFromURL(remoteIssue.ParentIssueURL)
	if issue.ParentIssueNumber != remoteParentNumber {
		if err := e.syncParentIssue(issue, remoteParentNumber); err != nil {
			logger.Warn("sync: failed to sync parent issue for #%d: %v", issue.Number, err)
			// Don't fail the whole sync - the main issue update succeeded
		}
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

// labelsEqual compares two label slices for equality (order-independent).
func labelsEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	aSet := make(map[string]bool)
	for _, label := range a {
		aSet[label] = true
	}
	for _, label := range b {
		if !aSet[label] {
			return false
		}
	}
	return true
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
		errMsgs := make([]string, len(syncErrors))
		for i, e := range syncErrors {
			errMsgs[i] = e.Error()
		}
		return fmt.Errorf("failed to sync %d pending comments: %s", len(syncErrors), strings.Join(errMsgs, "; "))
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
		errMsgs := make([]string, len(syncErrors))
		for i, e := range syncErrors {
			errMsgs[i] = e.Error()
		}
		return fmt.Errorf("failed to sync %d dirty comments: %s", len(syncErrors), strings.Join(errMsgs, "; "))
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
		cacheIssue := e.ghIssueToCacheIssue(ghIssue)
		if err := e.cache.UpsertIssue(cacheIssue); err != nil {
			logger.Warn("sync: failed to cache newly created issue #%d: %v", ghIssue.Number, err)
		}

		logger.Debug("sync: created issue #%d: %s", ghIssue.Number, ghIssue.Title)
	}

	if len(syncErrors) > 0 {
		errMsgs := make([]string, len(syncErrors))
		for i, e := range syncErrors {
			errMsgs[i] = e.Error()
		}
		return fmt.Errorf("failed to sync %d pending issues: %s", len(syncErrors), strings.Join(errMsgs, "; "))
	}

	return nil
}
