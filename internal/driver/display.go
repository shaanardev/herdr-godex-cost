package driver

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ReadDisplayMode is called on each pass so running workers pick up changes.
func ReadDisplayMode(stateDir string) (string, error) {
	data, err := os.ReadFile(filepath.Join(stateDir, "display-mode"))
	if errors.Is(err, os.ErrNotExist) {
		return "cost", nil
	}
	if err != nil {
		return "", fmt.Errorf("read display mode: %w", err)
	}
	mode := strings.TrimSpace(string(data))
	if mode != "cost" && mode != "credit" {
		return "", fmt.Errorf("invalid display mode %q: use cost or credit", mode)
	}
	return mode, nil
}

func SetDisplayMode(stateDir, mode string) error {
	if mode != "cost" && mode != "credit" {
		return fmt.Errorf("invalid display mode %q: use cost or credit", mode)
	}
	if err := os.MkdirAll(stateDir, 0700); err != nil {
		return err
	}
	file, err := os.CreateTemp(stateDir, ".display-mode-*")
	if err != nil {
		return err
	}
	defer os.Remove(file.Name())
	if _, err := file.WriteString(mode + "\n"); err != nil {
		file.Close()
		return err
	}
	if err := file.Close(); err != nil {
		return err
	}
	return os.Rename(file.Name(), filepath.Join(stateDir, "display-mode"))
}
