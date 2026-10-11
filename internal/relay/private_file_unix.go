//go:build linux || darwin

package relay

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// openPrivateFile walks from the filesystem root with no-follow directory
// handles. The returned descriptor therefore names the file whose ownership
// and mode were checked, even if another process races a pathname change.
func openPrivateFile(path string) (*os.File, error) {
	clean := filepath.Clean(path)
	if !filepath.IsAbs(clean) || clean == string(filepath.Separator) {
		return nil, fmt.Errorf("private file path must be absolute")
	}

	uid := uint32(os.Geteuid())
	fd, err := unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
	if err != nil {
		return nil, fmt.Errorf("open filesystem root: %w", err)
	}
	if err = checkPrivateDirectoryUnix(fd, string(filepath.Separator), uid); err != nil {
		unix.Close(fd)
		return nil, err
	}

	parts := strings.Split(strings.TrimPrefix(clean, string(filepath.Separator)), string(filepath.Separator))
	current := string(filepath.Separator)
	symlinks := 0
	for len(parts) > 0 {
		part := parts[0]
		parts = parts[1:]
		last := len(parts) == 0
		var lst unix.Stat_t
		if err = unix.Fstatat(fd, part, &lst, unix.AT_SYMLINK_NOFOLLOW); err != nil {
			unix.Close(fd)
			return nil, fmt.Errorf("inspect private path component %q: %w", part, err)
		}
		if uint32(lst.Mode)&unix.S_IFMT == unix.S_IFLNK {
			if last || lst.Uid != 0 {
				unix.Close(fd)
				return nil, fmt.Errorf("private path symlink is not allowed: %q", filepath.Join(current, part))
			}
			symlinks++
			if symlinks > 40 {
				unix.Close(fd)
				return nil, fmt.Errorf("private path has too many symlinks")
			}
			buf := make([]byte, 4096)
			n, readErr := unix.Readlinkat(fd, part, buf)
			if readErr != nil || n == len(buf) {
				unix.Close(fd)
				if readErr != nil {
					return nil, fmt.Errorf("read private path symlink %q: %w", filepath.Join(current, part), readErr)
				}
				return nil, fmt.Errorf("private path symlink target is too long: %q", filepath.Join(current, part))
			}
			target := string(buf[:n])
			resolved := filepath.Clean(target)
			if !filepath.IsAbs(target) {
				resolved = filepath.Clean(filepath.Join(current, target))
			}
			resolvedParts := []string{}
			if trimmed := strings.TrimPrefix(resolved, string(filepath.Separator)); trimmed != "" {
				resolvedParts = strings.Split(trimmed, string(filepath.Separator))
			}
			parts = append(resolvedParts, parts...)
			unix.Close(fd)
			fd, err = unix.Open(string(filepath.Separator), unix.O_RDONLY|unix.O_DIRECTORY|unix.O_CLOEXEC|unix.O_NOFOLLOW, 0)
			if err != nil {
				return nil, fmt.Errorf("reopen filesystem root: %w", err)
			}
			current = string(filepath.Separator)
			continue
		}
		flags := unix.O_RDONLY | unix.O_CLOEXEC | unix.O_NOFOLLOW
		if !last {
			flags |= unix.O_DIRECTORY
		}
		next, openErr := unix.Openat(fd, part, flags, 0)
		unix.Close(fd)
		if openErr != nil {
			return nil, fmt.Errorf("open private path component %q: %w", part, openErr)
		}
		fd = next
		current = filepath.Join(current, part)
		if !last {
			if err = checkPrivateDirectoryUnix(fd, current, uid); err != nil {
				unix.Close(fd)
				return nil, err
			}
		}
	}

	if err = checkPrivateRegularFileUnix(fd, uid); err != nil {
		unix.Close(fd)
		return nil, err
	}
	f := os.NewFile(uintptr(fd), clean)
	if f == nil {
		unix.Close(fd)
		return nil, fmt.Errorf("adopt private file descriptor")
	}
	return f, nil
}

func checkPrivateDirectoryUnix(fd int, path string, uid uint32) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fmt.Errorf("inspect private path directory %q: %w", path, err)
	}
	mode := uint32(st.Mode)
	if mode&unix.S_IFMT != unix.S_IFDIR {
		return fmt.Errorf("private path component is not a directory: %q", path)
	}
	if st.Uid != 0 && st.Uid != uid {
		return fmt.Errorf("private path directory is not owned by the current user or root: %q", path)
	}
	// A trusted sticky directory such as /tmp cannot be used by another user
	// to replace this user's children. Other group/world-writable directories
	// are unsafe: a matching primary group does not prove it has no other users.
	stickyRoot := st.Uid == 0 && mode&unix.S_ISVTX != 0
	if mode&0o002 != 0 && !stickyRoot {
		return fmt.Errorf("private path directory is writable by another user: %q", path)
	}
	if mode&0o020 != 0 && !stickyRoot {
		return fmt.Errorf("private path directory is writable by another user: %q", path)
	}
	return nil
}

func checkPrivateRegularFileUnix(fd int, uid uint32) error {
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		return fmt.Errorf("inspect private file: %w", err)
	}
	mode := uint32(st.Mode)
	if mode&unix.S_IFMT != unix.S_IFREG {
		return fmt.Errorf("private file must be regular")
	}
	if st.Uid != uid {
		return fmt.Errorf("private file must be owned by the current user")
	}
	if mode&0o077 != 0 {
		return fmt.Errorf("private file permissions must not grant group or other access")
	}
	return nil
}
