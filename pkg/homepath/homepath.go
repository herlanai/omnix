// Package homepath resolves the user data root using explicit configuration,
// then OMNIX_HOME, then ~/.omnix. It supports tilde expansion and symbolic display,
// matching util/home-paths upstream.
package homepath

import (
	"os"
	"path/filepath"
	"strings"
)

// HomeDirName is the default Harness home directory name.
const HomeDirName = ".omnix"

// EnvName is the environment variable overriding the default home.
const EnvName = "OMNIX_HOME"

// ExpandHome expands leading ~, ~/, or ~\ to the OS home; other prefixes are unchanged.
func ExpandHome(path string) (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	if path == "~" {
		return home, nil
	}
	if strings.HasPrefix(path, "~/") || strings.HasPrefix(path, `~\`) {
		return filepath.Join(home, path[2:]), nil
	}
	return path, nil
}

// Resolve prefers nonempty configured, then nonblank OMNIX_HOME in env,
// then the default ~/.omnix.
func Resolve(configured string, env map[string]string) (string, error) {
	var selected string
	switch {
	case strings.TrimSpace(configured) != "":
		selected = configured
	case strings.TrimSpace(env[EnvName]) != "":
		selected = env[EnvName]
	default:
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		selected = filepath.Join(home, HomeDirName)
	}
	expanded, err := ExpandHome(selected)
	if err != nil {
		return "", err
	}
	return filepath.Abs(expanded)
}

// Display renders the default home as ~/.omnix and custom homes as $OMNIX_HOME.
func Display(resolvedHome string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "$" + EnvName
	}
	def, _ := filepath.Abs(filepath.Join(home, HomeDirName))
	if filepath.Clean(resolvedHome) == def {
		return "~/" + HomeDirName
	}
	return "$" + EnvName
}
