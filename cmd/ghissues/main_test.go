package main

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestValidateRepo(t *testing.T) {
	tests := []struct {
		name        string
		repo        string
		wantOwner   string
		wantName    string
		wantErr     bool
		errContains string
	}{
		{
			name:      "valid owner/repo",
			repo:      "owner/repo",
			wantOwner: "owner",
			wantName:  "repo",
			wantErr:   false,
		},
		{
			name:      "valid with hyphen",
			repo:      "my-org/my-repo",
			wantOwner: "my-org",
			wantName:  "my-repo",
			wantErr:   false,
		},
		{
			name:      "valid with underscore",
			repo:      "my_org/my_repo",
			wantOwner: "my_org",
			wantName:  "my_repo",
			wantErr:   false,
		},
		{
			name:      "valid with numbers",
			repo:      "user123/repo456",
			wantOwner: "user123",
			wantName:  "repo456",
			wantErr:   false,
		},
		{
			name:      "valid with multiple slashes (only first split)",
			repo:      "owner/repo/extra",
			wantOwner: "owner",
			wantName:  "repo/extra",
			wantErr:   false,
		},
		{
			name:        "invalid no slash",
			repo:        "invalid",
			wantErr:     true,
			errContains: "must be in the format owner/repo",
		},
		{
			name:        "invalid empty owner",
			repo:        "/repo",
			wantErr:     true,
			errContains: "owner and repo cannot be empty",
		},
		{
			name:        "invalid empty repo",
			repo:        "owner/",
			wantErr:     true,
			errContains: "owner and repo cannot be empty",
		},
		{
			name:        "invalid both empty",
			repo:        "/",
			wantErr:     true,
			errContains: "owner and repo cannot be empty",
		},
		{
			name:        "invalid empty string",
			repo:        "",
			wantErr:     true,
			errContains: "must be in the format owner/repo",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			owner, name, err := validateRepo(tt.repo)

			if tt.wantErr {
				if err == nil {
					t.Errorf("validateRepo(%q) expected error, got nil", tt.repo)
					return
				}
				if tt.errContains != "" && !strings.Contains(err.Error(), tt.errContains) {
					t.Errorf("validateRepo(%q) error = %q, want error containing %q", tt.repo, err.Error(), tt.errContains)
				}
				return
			}

			if err != nil {
				t.Errorf("validateRepo(%q) unexpected error: %v", tt.repo, err)
				return
			}

			if owner != tt.wantOwner {
				t.Errorf("validateRepo(%q) owner = %q, want %q", tt.repo, owner, tt.wantOwner)
			}
			if name != tt.wantName {
				t.Errorf("validateRepo(%q) name = %q, want %q", tt.repo, name, tt.wantName)
			}
		})
	}
}

func TestEnsureMountpoint_ExistsAndIsDirectory(t *testing.T) {
	tmpDir := t.TempDir()
	mountpoint := filepath.Join(tmpDir, "existing-dir")

	// Create the directory first
	if err := os.Mkdir(mountpoint, 0755); err != nil {
		t.Fatalf("failed to create test directory: %v", err)
	}

	created, err := ensureMountpoint(mountpoint)
	if err != nil {
		t.Errorf("ensureMountpoint() unexpected error: %v", err)
	}
	if created {
		t.Error("ensureMountpoint() created = true, want false for existing directory")
	}
}

func TestEnsureMountpoint_DoesNotExist_CreatesIt(t *testing.T) {
	tmpDir := t.TempDir()
	mountpoint := filepath.Join(tmpDir, "new-dir")

	created, err := ensureMountpoint(mountpoint)
	if err != nil {
		t.Errorf("ensureMountpoint() unexpected error: %v", err)
	}
	if !created {
		t.Error("ensureMountpoint() created = false, want true for new directory")
	}

	// Verify the directory was created
	info, err := os.Stat(mountpoint)
	if err != nil {
		t.Fatalf("directory was not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("created path is not a directory")
	}
}

func TestEnsureMountpoint_DoesNotExist_CreatesNestedPath(t *testing.T) {
	tmpDir := t.TempDir()
	mountpoint := filepath.Join(tmpDir, "nested", "path", "dir")

	created, err := ensureMountpoint(mountpoint)
	if err != nil {
		t.Errorf("ensureMountpoint() unexpected error: %v", err)
	}
	if !created {
		t.Error("ensureMountpoint() created = false, want true for new nested directory")
	}

	// Verify the directory was created
	info, err := os.Stat(mountpoint)
	if err != nil {
		t.Fatalf("nested directory was not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("created path is not a directory")
	}
}

func TestEnsureMountpoint_ExistsButIsFile_ReturnsError(t *testing.T) {
	tmpDir := t.TempDir()
	mountpoint := filepath.Join(tmpDir, "existing-file")

	// Create a file instead of directory
	f, err := os.Create(mountpoint)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	f.Close()

	created, err := ensureMountpoint(mountpoint)
	if err == nil {
		t.Error("ensureMountpoint() expected error for file, got nil")
	}
	if created {
		t.Error("ensureMountpoint() created = true, want false for error case")
	}
	if !strings.Contains(err.Error(), "not a directory") {
		t.Errorf("ensureMountpoint() error = %q, want error containing 'not a directory'", err.Error())
	}
}

func TestEnsureMountpoint_CannotCreate_ReturnsError(t *testing.T) {
	// Skip this test if running as root (root can create directories anywhere)
	if os.Getuid() == 0 {
		t.Skip("skipping test when running as root")
	}

	// Try to create a directory in a location we cannot write to
	mountpoint := "/nonexistent-root-dir/subdir"

	created, err := ensureMountpoint(mountpoint)
	if err == nil {
		// Cleanup if somehow it succeeded
		os.RemoveAll(mountpoint)
		t.Error("ensureMountpoint() expected error for unwritable path, got nil")
	}
	if created {
		t.Error("ensureMountpoint() created = true, want false for error case")
	}
}

func TestGetCachePath_ValidRepo(t *testing.T) {
	// Save original HOME and restore after test
	originalHome := os.Getenv("HOME")
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	cachePath, err := getCachePath("myowner", "myrepo")
	if err != nil {
		t.Fatalf("getCachePath() unexpected error: %v", err)
	}

	// Check the path format
	expectedSuffix := filepath.Join(".cache", "ghissues", "myowner_myrepo.db")
	if !strings.HasSuffix(cachePath, expectedSuffix) {
		t.Errorf("getCachePath() = %q, want path ending with %q", cachePath, expectedSuffix)
	}

	// Verify the cache directory was created
	cacheDir := filepath.Dir(cachePath)
	info, err := os.Stat(cacheDir)
	if err != nil {
		t.Fatalf("cache directory was not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("cache path parent is not a directory")
	}
}

func TestGetCachePath_SpecialCharactersInRepoName(t *testing.T) {
	// Save original HOME and restore after test
	originalHome := os.Getenv("HOME")
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	tests := []struct {
		owner    string
		repoName string
		wantFile string
	}{
		{"owner", "repo", "owner_repo.db"},
		{"my-org", "my-repo", "my-org_my-repo.db"},
		{"org123", "repo456", "org123_repo456.db"},
	}

	for _, tt := range tests {
		t.Run(tt.owner+"/"+tt.repoName, func(t *testing.T) {
			cachePath, err := getCachePath(tt.owner, tt.repoName)
			if err != nil {
				t.Fatalf("getCachePath() unexpected error: %v", err)
			}

			filename := filepath.Base(cachePath)
			if filename != tt.wantFile {
				t.Errorf("getCachePath() filename = %q, want %q", filename, tt.wantFile)
			}
		})
	}
}

func TestGetCachePath_CreatesNestedCacheDirectory(t *testing.T) {
	// Save original HOME and restore after test
	originalHome := os.Getenv("HOME")
	tmpDir := t.TempDir()
	os.Setenv("HOME", tmpDir)
	defer os.Setenv("HOME", originalHome)

	cachePath, err := getCachePath("owner", "repo")
	if err != nil {
		t.Fatalf("getCachePath() unexpected error: %v", err)
	}

	// Verify ~/.cache/ghissues was created
	expectedCacheDir := filepath.Join(tmpDir, ".cache", "ghissues")
	info, err := os.Stat(expectedCacheDir)
	if err != nil {
		t.Fatalf("cache directory was not created: %v", err)
	}
	if !info.IsDir() {
		t.Error("cache directory is not a directory")
	}

	// Verify cache path is inside that directory
	if !strings.HasPrefix(cachePath, expectedCacheDir) {
		t.Errorf("cachePath = %q, want path starting with %q", cachePath, expectedCacheDir)
	}
}

func TestGetUnmountCommand_Darwin(t *testing.T) {
	if runtime.GOOS != "darwin" {
		t.Skip("skipping macOS-specific test on non-darwin platform")
	}

	cmd := getUnmountCommand("/mnt/test")

	if cmd.Path != "/sbin/umount" && !strings.HasSuffix(cmd.Path, "/umount") {
		t.Errorf("getUnmountCommand() path = %q, want path ending with 'umount'", cmd.Path)
	}

	// Check args
	args := cmd.Args
	if len(args) != 2 {
		t.Fatalf("getUnmountCommand() args length = %d, want 2", len(args))
	}
	if args[1] != "/mnt/test" {
		t.Errorf("getUnmountCommand() args[1] = %q, want '/mnt/test'", args[1])
	}
}

func TestGetUnmountCommand_Linux(t *testing.T) {
	if runtime.GOOS != "linux" {
		t.Skip("skipping Linux-specific test on non-linux platform")
	}

	cmd := getUnmountCommand("/mnt/test")

	if !strings.HasSuffix(cmd.Path, "/fusermount") && cmd.Path != "fusermount" {
		// On some systems the path might be resolved, on others not
		if cmd.Args[0] != "fusermount" {
			t.Errorf("getUnmountCommand() should use fusermount, got path=%q args[0]=%q", cmd.Path, cmd.Args[0])
		}
	}

	// Check args - should be: fusermount -u /mnt/test
	args := cmd.Args
	if len(args) != 3 {
		t.Fatalf("getUnmountCommand() args length = %d, want 3", len(args))
	}
	if args[1] != "-u" {
		t.Errorf("getUnmountCommand() args[1] = %q, want '-u'", args[1])
	}
	if args[2] != "/mnt/test" {
		t.Errorf("getUnmountCommand() args[2] = %q, want '/mnt/test'", args[2])
	}
}

func TestGetUnmountCommand_ReturnsValidCommand(t *testing.T) {
	// This test works on any platform
	cmd := getUnmountCommand("/some/path")

	if cmd == nil {
		t.Fatal("getUnmountCommand() returned nil")
	}

	// Should have at least the command and the mountpoint
	if len(cmd.Args) < 2 {
		t.Errorf("getUnmountCommand() args length = %d, want at least 2", len(cmd.Args))
	}

	// The mountpoint should be in the args
	found := false
	for _, arg := range cmd.Args {
		if arg == "/some/path" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("getUnmountCommand() args = %v, should contain '/some/path'", cmd.Args)
	}
}

// Test CLI argument validation through cobra

func TestMountCmd_RequiresTwoArgs(t *testing.T) {
	// Reset command for clean state
	rootCmd.SetArgs([]string{"mount"})
	err := rootCmd.Execute()

	if err == nil {
		t.Error("mount command should fail with no arguments")
	}
}

func TestMountCmd_RejectsOneArg(t *testing.T) {
	rootCmd.SetArgs([]string{"mount", "owner/repo"})
	err := rootCmd.Execute()

	if err == nil {
		t.Error("mount command should fail with only one argument")
	}
}

func TestUnmountCmd_RequiresOneArg(t *testing.T) {
	rootCmd.SetArgs([]string{"unmount"})
	err := rootCmd.Execute()

	if err == nil {
		t.Error("unmount command should fail with no arguments")
	}
}

func TestUnmountCmd_RejectsTwoArgs(t *testing.T) {
	rootCmd.SetArgs([]string{"unmount", "/path1", "/path2"})
	err := rootCmd.Execute()

	if err == nil {
		t.Error("unmount command should fail with two arguments")
	}
}

// Test error messages are user-friendly

func TestValidateRepo_ErrorMessageIncludesInput(t *testing.T) {
	_, _, err := validateRepo("badformat")
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Error should include the bad input for debugging
	if !strings.Contains(err.Error(), "badformat") {
		t.Errorf("error message should include the input, got: %v", err)
	}
}

func TestEnsureMountpoint_ErrorMessageIncludesPath(t *testing.T) {
	tmpDir := t.TempDir()
	filePath := filepath.Join(tmpDir, "afile")

	// Create a file
	f, err := os.Create(filePath)
	if err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}
	f.Close()

	_, err = ensureMountpoint(filePath)
	if err == nil {
		t.Fatal("expected error, got nil")
	}

	// Error should include the path for debugging
	if !strings.Contains(err.Error(), filePath) {
		t.Errorf("error message should include the path, got: %v", err)
	}
}

// Benchmark tests

func BenchmarkValidateRepo(b *testing.B) {
	for i := 0; i < b.N; i++ {
		validateRepo("owner/repo")
	}
}

func BenchmarkEnsureMountpoint_ExistingDir(b *testing.B) {
	tmpDir := b.TempDir()
	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		ensureMountpoint(tmpDir)
	}
}
