package gitindex

import (
	"bytes"
	"errors"
	"fmt"
	"maps"
	"os/exec"
	"slices"
	"strings"
)

func sortedKeys(settings map[string]string) []string {
	return slices.Sorted(maps.Keys(settings))
}

func cloneConfigArgs(settings map[string]string) []string {
	args := make([]string, 0, len(settings)*2)
	for _, key := range sortedKeys(settings) {
		if value := settings[key]; value != "" {
			args = append(args, "--config", key+"="+value)
		}
	}
	return args
}

// updateZoektGitConfig applies zoekt.* settings to an existing clone.
// It returns whether the repository config changed.
func updateZoektGitConfig(repoDest string, settings map[string]string) (bool, error) {
	changed := false
	for _, key := range sortedKeys(settings) {
		updated, err := syncGitConfigOption(repoDest, key, settings[key])
		if err != nil {
			return false, err
		}
		changed = changed || updated
	}
	return changed, nil
}

func syncGitConfigOption(repoDest, key, value string) (bool, error) {
	current, ok, err := repoConfigValue(repoDest, key)
	if err != nil {
		return false, err
	}

	if value == "" {
		if !ok {
			return false, nil
		}
		if err := unsetRepoConfigValue(repoDest, key); err != nil {
			return false, err
		}
		return true, nil
	}

	if ok && current == value {
		return false, nil
	}
	if err := setRepoConfigValue(repoDest, key, value); err != nil {
		return false, err
	}
	return true, nil
}

func repoConfigValue(repoDest, key string) (string, bool, error) {
	cmd := exec.Command("git", "-C", repoDest, "config", "--get", key)
	var out bytes.Buffer
	cmd.Stdout = &out
	if err := cmd.Run(); err == nil {
		return strings.TrimSuffix(out.String(), "\n"), true, nil
	} else {
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) && exitErr.ExitCode() == 1 {
			return "", false, nil
		}
		return "", false, fmt.Errorf("git config --get %q: %w", key, err)
	}
}

func setRepoConfigValue(repoDest, key, value string) error {
	if err := exec.Command("git", "-C", repoDest, "config", "--replace-all", key, value).Run(); err != nil {
		return fmt.Errorf("git config --replace-all %q: %w", key, err)
	}
	return nil
}

func unsetRepoConfigValue(repoDest, key string) error {
	if err := exec.Command("git", "-C", repoDest, "config", "--unset-all", key).Run(); err != nil {
		return fmt.Errorf("git config --unset-all %q: %w", key, err)
	}
	return nil
}
