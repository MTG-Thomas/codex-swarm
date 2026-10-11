package relay

import "os"

// OpenPrivateFile returns a stable validated handle to an owner-private file.
// The platform implementations reject unsafe owners, permissions or ACLs,
// symlinks/reparse points, replaceable parents, and non-regular final objects.
func OpenPrivateFile(path string) (*os.File, error) {
	return openPrivateFile(path)
}
