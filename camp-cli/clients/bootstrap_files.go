package clients

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Reject symlinks in all managed descendants, including backup destinations.
func safeInstallPath(root, path string) error {
	resolvedRoot, err := canonicalInstallRoot(root)
	if err != nil {
		return err
	}
	if resolvedRoot != root {
		return fmt.Errorf("installation root changed through a symlink: %s", root)
	}
	rel, err := filepath.Rel(root, path)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("installation path escapes root")
	}
	current := root
	for _, part := range strings.Split(rel, string(filepath.Separator)) {
		current = filepath.Join(current, part)
		info, err := os.Lstat(current)
		if os.IsNotExist(err) {
			continue
		}
		if err != nil {
			return err
		}
		if info.Mode()&os.ModeSymlink != 0 {
			return fmt.Errorf("refusing symlink in installation path: %s", current)
		}
		if current != path && !info.IsDir() {
			return fmt.Errorf("installation parent is not a directory: %s", current)
		}
		if current == path && !info.Mode().IsRegular() {
			return fmt.Errorf("installation target is not a regular file: %s", current)
		}
	}
	if info, err := os.Lstat(path + ".camp-backup"); err == nil && !info.Mode().IsRegular() {
		return fmt.Errorf("unsafe backup target: %s", path)
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func applyInstall(p installation) (InstallResult, error) {
	var applied []string
	for _, edit := range p.files {
		if err := safeInstallPath(edit.root, edit.Path); err != nil {
			return p.result, fmt.Errorf("installation stopped (already updated: %v): %w", applied, err)
		}
		if bytes.Equal(edit.before, []byte(edit.Content)) {
			info, err := os.Stat(edit.Path)
			if err == nil && info.Mode().Perm() == edit.Mode {
				continue
			}
		}
		// Revalidate after preview and before mutation; do not overwrite edits made meanwhile.
		current, err := os.ReadFile(edit.Path)
		if err != nil && !os.IsNotExist(err) {
			return p.result, err
		}
		if !bytes.Equal(current, edit.before) {
			return p.result, fmt.Errorf("%s changed during installation; rerun", edit.Path)
		}
		if err = os.MkdirAll(filepath.Dir(edit.Path), 0755); err == nil && len(edit.before) > 0 {
			err = atomicInstallWrite(edit.Path+".camp-backup", edit.before, 0600)
		}
		if err == nil {
			err = atomicInstallWrite(edit.Path, []byte(edit.Content), edit.Mode)
		}
		if err != nil {
			return p.result, fmt.Errorf("install %s (already updated: %v): %w", edit.Path, applied, err)
		}
		applied = append(applied, edit.Path)
	}
	return p.result, nil
}
func atomicInstallWrite(path string, body []byte, mode os.FileMode) error {
	file, err := os.CreateTemp(filepath.Dir(path), ".camp-install-*")
	if err != nil {
		return err
	}
	tmp := file.Name()
	defer os.Remove(tmp)
	if err = file.Chmod(mode); err == nil {
		_, err = file.Write(body)
	}
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return err
	}
	return os.Rename(tmp, path)
}
