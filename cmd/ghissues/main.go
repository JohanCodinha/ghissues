// Package main provides the CLI entrypoint for ghissues.
package main

import (
	"fmt"
	"os"
	"strings"

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

	fmt.Printf("mounting %s to %s\n", repo, mountpoint)
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

	fmt.Printf("unmounting %s\n", mountpoint)
	return nil
}
