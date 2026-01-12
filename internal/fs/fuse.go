// Package fs provides a FUSE filesystem for GitHub issues.
package fs

// FS represents the FUSE filesystem for GitHub issues.
type FS struct {
	mountpoint string
	repo       string
}

// New creates a new FUSE filesystem.
func New(mountpoint, repo string) *FS {
	return &FS{
		mountpoint: mountpoint,
		repo:       repo,
	}
}

// Mount mounts the filesystem.
func (f *FS) Mount() error {
	// TODO: Implement FUSE mount
	return nil
}

// Unmount unmounts the filesystem.
func (f *FS) Unmount() error {
	// TODO: Implement FUSE unmount
	return nil
}
