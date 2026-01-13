// Package main provides the CLI entrypoint for ghissues.
package main

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/JohanCodinha/ghissues/internal/cache"
	"github.com/JohanCodinha/ghissues/internal/fs"
	"github.com/JohanCodinha/ghissues/internal/gh"
	"github.com/JohanCodinha/ghissues/internal/logger"
	"github.com/JohanCodinha/ghissues/internal/sync"
	"github.com/spf13/cobra"
)

// CLI flags for logging configuration
var (
	logLevel string
	logFile  string
	quiet    bool
)

// validateRepo validates the repository format and returns the owner and repo name.
// The format must be "owner/repo" where neither owner nor repo is empty.
func validateRepo(repo string) (owner, name string, err error) {
	if !strings.Contains(repo, "/") {
		return "", "", fmt.Errorf("invalid repository format %q: must be in the format owner/repo", repo)
	}

	parts := strings.SplitN(repo, "/", 2)
	if parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid repository format %q: owner and repo cannot be empty", repo)
	}

	return parts[0], parts[1], nil
}

// ensureMountpoint ensures that the mountpoint exists and is a directory.
// If the mountpoint doesn't exist, it will be created.
// Returns true if the mountpoint was created, false if it already existed.
func ensureMountpoint(path string) (created bool, err error) {
	info, err := os.Stat(path)
	if err != nil {
		if os.IsNotExist(err) {
			if err := os.MkdirAll(path, 0755); err != nil {
				return false, fmt.Errorf("failed to create mountpoint %q: %w", path, err)
			}
			return true, nil
		}
		return false, fmt.Errorf("cannot access mountpoint %q: %w", path, err)
	}

	if !info.IsDir() {
		return false, fmt.Errorf("mountpoint %q is not a directory", path)
	}

	return false, nil
}

// getCachePath returns the path to the cache database file for the given repository.
// The cache is stored at ~/.cache/ghissues/{owner}_{repo}.db
func getCachePath(owner, repoName string) (string, error) {
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("failed to get home directory: %w", err)
	}

	cacheDir := filepath.Join(homeDir, ".cache", "ghissues")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return "", fmt.Errorf("failed to create cache directory: %w", err)
	}

	return filepath.Join(cacheDir, fmt.Sprintf("%s_%s.db", owner, repoName)), nil
}

// getUnmountCommand returns the appropriate system unmount command for the current OS.
func getUnmountCommand(mountpoint string) *exec.Cmd {
	if runtime.GOOS == "darwin" {
		return exec.Command("umount", mountpoint)
	}
	return exec.Command("fusermount", "-u", mountpoint)
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		os.Exit(1)
	}
}

var rootCmd = &cobra.Command{
	Use:   "ghissues",
	Short: "Mount GitHub issues as a FUSE filesystem",
	Long: `ghissues mounts a GitHub repository's issues as markdown files
in a FUSE filesystem, allowing you to read and edit issues
using your favorite text editor.`,
}

var mountCmd = &cobra.Command{
	Use:   "mount <owner/repo> <mountpoint>",
	Short: "Mount a repository's issues as a filesystem",
	Long: `Mount a GitHub repository's issues as markdown files at the specified mountpoint.

The repository must be specified in the format "owner/repo".
The mountpoint must be an existing directory.`,
	Args: cobra.ExactArgs(2),
	RunE: runMount,
}

var unmountCmd = &cobra.Command{
	Use:   "unmount <mountpoint>",
	Short: "Unmount a previously mounted filesystem",
	Long: `Unmount a ghissues filesystem and flush any pending changes to GitHub.

The mountpoint must be an existing directory where ghissues is mounted.`,
	Args: cobra.ExactArgs(1),
	RunE: runUnmount,
}

func init() {
	// Add logging flags to mount command
	mountCmd.Flags().StringVar(&logLevel, "log-level", "info", "Log level (debug, info, warn, error)")
	mountCmd.Flags().StringVar(&logFile, "log-file", "", "Path to log file (logs to stderr if not set)")
	mountCmd.Flags().BoolVarP(&quiet, "quiet", "q", false, "Suppress non-error output")

	rootCmd.AddCommand(mountCmd)
	rootCmd.AddCommand(unmountCmd)
}

func runMount(cmd *cobra.Command, args []string) error {
	repo := args[0]
	mountpoint := args[1]

	// Configure logging based on CLI flags
	if err := configureLogging(); err != nil {
		return err
	}
	defer logger.Close()

	// Validate repo format
	owner, repoName, err := validateRepo(repo)
	if err != nil {
		return err
	}

	// Create mountpoint if it doesn't exist
	created, err := ensureMountpoint(mountpoint)
	if err != nil {
		return err
	}
	if created {
		logger.Info("created mountpoint %s", mountpoint)
	}

	// 1. Get GitHub auth token
	token, err := gh.GetToken()
	if err != nil {
		return fmt.Errorf("failed to get GitHub token: %w\nRun 'gh auth login' to authenticate", err)
	}
	logger.Info("authenticated with GitHub")

	// 2. Create GitHub client
	client := gh.New(token)

	// 3. Determine cache path: ~/.cache/ghissues/{owner}_{repo}.db
	cachePath, err := getCachePath(owner, repoName)
	if err != nil {
		return err
	}

	// 4. Initialize cache
	cacheDB, err := cache.InitDB(cachePath)
	if err != nil {
		return fmt.Errorf("failed to initialize cache: %w", err)
	}
	logger.Info("cache initialized at %s", cachePath)

	// 5. Create sync engine with 500ms debounce
	engine, err := sync.NewEngine(cacheDB, client, repo, 500)
	if err != nil {
		cacheDB.Close()
		return fmt.Errorf("failed to create sync engine: %w", err)
	}

	// 6. Run initial sync
	logger.Info("syncing issues from %s...", repo)
	if err := engine.InitialSync(); err != nil {
		// Log warning but continue in offline mode
		logger.Warn("initial sync failed: %v", err)
		logger.Warn("continuing in offline mode with cached data")
	}

	// 6b. Retry any pending items from previous session
	if err := engine.SyncNow(); err != nil {
		logger.Warn("failed to sync pending items: %v", err)
	}

	// 7. Create FS with onDirty callback to trigger sync and status provider
	filesystem := fs.NewFS(cacheDB, repo, mountpoint, func() {
		engine.TriggerSync()
	}, engine)

	// 8. Mount (blocks until unmount)
	logger.Info("mounting %s to %s", repo, mountpoint)
	logger.Info("press Ctrl+C to unmount")
	mountErr := filesystem.Mount()

	// 9. Cleanup on return (after unmount)
	logger.Info("unmounting...")

	// Flush any pending changes
	if err := engine.SyncNow(); err != nil {
		logger.Warn("failed to sync pending changes: %v", err)
	}

	// Stop the sync engine
	engine.Stop()

	// Close the cache
	if err := cacheDB.Close(); err != nil {
		logger.Warn("failed to close cache: %v", err)
	}

	if mountErr != nil {
		return fmt.Errorf("mount error: %w", mountErr)
	}

	logger.Info("unmounted successfully")
	return nil
}

// configureLogging sets up the logger based on CLI flags.
func configureLogging() error {
	// Parse and set log level
	level, err := logger.ParseLevel(logLevel)
	if err != nil {
		return err
	}

	// If quiet mode, only show errors
	if quiet {
		level = logger.LevelError
	}

	logger.SetLevel(level)

	// Set up log file if specified
	if logFile != "" {
		if err := logger.SetLogFile(logFile); err != nil {
			return fmt.Errorf("failed to set log file: %w", err)
		}
	}

	return nil
}

func runUnmount(cmd *cobra.Command, args []string) error {
	mountpoint := args[0]

	// Validate mountpoint exists
	info, err := os.Stat(mountpoint)
	if err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("mountpoint %q does not exist", mountpoint)
		}
		return fmt.Errorf("cannot access mountpoint %q: %w", mountpoint, err)
	}

	if !info.IsDir() {
		return fmt.Errorf("mountpoint %q is not a directory", mountpoint)
	}

	// Get absolute path for the mountpoint
	absMountpoint, err := filepath.Abs(mountpoint)
	if err != nil {
		return fmt.Errorf("failed to get absolute path: %w", err)
	}

	fmt.Printf("unmounting %s\n", absMountpoint)

	// Call the appropriate system unmount command based on platform
	unmountCommand := getUnmountCommand(absMountpoint)
	unmountCommand.Stdout = os.Stdout
	unmountCommand.Stderr = os.Stderr

	if err := unmountCommand.Run(); err != nil {
		return fmt.Errorf("failed to unmount: %w", err)
	}

	fmt.Println("unmounted successfully")
	return nil
}
