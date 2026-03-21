package tools

import (
	"path/filepath"
	"strings"
)

// skipDirs is the set of directory names to exclude from search results.
var skipDirs = map[string]bool{
	".git":         true,
	"node_modules": true,
	".hg":          true,
	".svn":         true,
}

// shouldSkipDir returns true if the directory name should be excluded.
func shouldSkipDir(name string) bool {
	return skipDirs[name]
}

// shouldSkipPath returns true if any path component is a skipped directory.
func shouldSkipPath(path string) bool {
	parts := strings.Split(filepath.ToSlash(path), "/")
	for _, part := range parts {
		if skipDirs[part] {
			return true
		}
	}
	return false
}
