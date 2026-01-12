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
	"github.com/JohanCodinha/ghissues/internal/sync"
	"github.com/spf13/cobra"
)

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
	rootCmd.AddCommand(mountCmd)
	rootCmd.AddCommand(unmountCmd)
}

func runMount(cmd *cobra.Command, args []string) error {
	repo := args[0]
	mountpoint := args[1]

	// Validate repo format contains "/"
	if !strings.Contains(repo, "/") {
		return fmt.Errorf("invalid repository format %q: must be in the format owner/repo", repo)
	}

	parts := strings.SplitN(repo, "/", 2)
	if parts[0] == "" || parts[1] == "" {
		return fmt.Errorf("invalid repository format %q: owner and repo cannot be empty", repo)
	}

	// Create mountpoint if it doesn't exist
	info, err := os.Stat(mountpoint)
	if err != nil {
		if os.IsNotExist(err) {
			if err := os.MkdirAll(mountpoint, 0755); err != nil {
				return fmt.Errorf("failed to create mountpoint %q: %w", mountpoint, err)
			}
			fmt.Printf("created mountpoint %s\n", mountpoint)
		} else {
			return fmt.Errorf("cannot access mountpoint %q: %w", mountpoint, err)
		}
	} else if !info.IsDir() {
		return fmt.Errorf("mountpoint %q is not a directory", mountpoint)
	}

	// 1. Get GitHub auth token
	token, err := gh.GetToken()
	if err != nil {
		return fmt.Errorf("failed to get GitHub token: %w\nRun 'gh auth login' to authenticate", err)
	}
	fmt.Println("authenticated with GitHub")

	// 2. Create GitHub client
	client := gh.New(token)

	// 3. Determine cache path: ~/.cache/ghissues/{owner}_{repo}.db
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get home directory: %w", err)
	}
	cacheDir := filepath.Join(homeDir, ".cache", "ghissues")
	if err := os.MkdirAll(cacheDir, 0755); err != nil {
		return fmt.Errorf("failed to create cache directory: %w", err)
	}
	cachePath := filepath.Join(cacheDir, fmt.Sprintf("%s_%s.db", parts[0], parts[1]))

	// 4. Initialize cache
	cacheDB, err := cache.InitDB(cachePath)
	if err != nil {
		return fmt.Errorf("failed to initialize cache: %w", err)
	}
	fmt.Printf("cache initialized at %s\n", cachePath)

	// 5. Create sync engine with 500ms debounce
	engine, err := sync.NewEngine(cacheDB, client, repo, 500)
	if err != nil {
		cacheDB.Close()
		return fmt.Errorf("failed to create sync engine: %w", err)
	}

	// 6. Run initial sync
	fmt.Printf("syncing issues from %s...\n", repo)
	if err := engine.InitialSync(); err != nil {
		// Print error but continue in offline mode
		fmt.Fprintf(os.Stderr, "warning: initial sync failed: %v\n", err)
		fmt.Fprintf(os.Stderr, "continuing in offline mode with cached data\n")
	}

	// 7. Create FS with onDirty callback to trigger sync
	filesystem := fs.NewFS(cacheDB, repo, mountpoint, func() {
		engine.TriggerSync()
	})

	// 8. Mount (blocks until unmount)
	fmt.Printf("mounting %s to %s\n", repo, mountpoint)
	fmt.Println("press Ctrl+C to unmount")
	mountErr := filesystem.Mount()

	// 9. Cleanup on return (after unmount)
	fmt.Println("unmounting...")

	// Flush any pending changes
	if err := engine.SyncNow(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to sync pending changes: %v\n", err)
	}

	// Stop the sync engine
	engine.Stop()

	// Close the cache
	if err := cacheDB.Close(); err != nil {
		fmt.Fprintf(os.Stderr, "warning: failed to close cache: %v\n", err)
	}

	if mountErr != nil {
		return fmt.Errorf("mount error: %w", mountErr)
	}

	fmt.Println("unmounted successfully")
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
	var unmountCmd *exec.Cmd
	if runtime.GOOS == "darwin" {
		// macOS uses umount
		unmountCmd = exec.Command("umount", absMountpoint)
	} else {
		// Linux uses fusermount -u
		unmountCmd = exec.Command("fusermount", "-u", absMountpoint)
	}

	unmountCmd.Stdout = os.Stdout
	unmountCmd.Stderr = os.Stderr

	if err := unmountCmd.Run(); err != nil {
		return fmt.Errorf("failed to unmount: %w", err)
	}

	fmt.Println("unmounted successfully")
	return nil
}
