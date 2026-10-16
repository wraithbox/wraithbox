package platform

import (
	"fmt"
	"os/user"
	"path/filepath"
)

// homeOf returns the home directory of a user database entry. Each OS
// file passes it the result of its own lookup, which never reads $HOME
// (S05-04-naming, "Resolving the key").
func homeOf(u *user.User, err error) (string, error) {
	if err != nil {
		return "", fmt.Errorf("look up the current user in the user database: %w", err)
	}
	if !filepath.IsAbs(u.HomeDir) {
		return "", fmt.Errorf("the user database gives user %s the home directory %q, which is not an absolute path", u.Username, u.HomeDir)
	}
	return u.HomeDir, nil
}
