// Package home resolves CUWAN_HOME (default ~/.cuwan) and keeps it private.
package home

import (
	"fmt"
	"os"
	"path/filepath"
)

// Dir is the folder for the sealed store and runtime files.
// Override with CUWAN_HOME.
func Dir() (string, error) {
	if h := os.Getenv("CUWAN_HOME"); h != "" {
		return filepath.Clean(h), nil
	}
	d, err := os.UserHomeDir()
	if err != nil {
		return "", fmt.Errorf("home: user home: %w", err)
	}
	return filepath.Join(d, ".cuwan"), nil
}

// Ensure creates Dir with mode 0700 if missing.
func Ensure() (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	if err := os.MkdirAll(d, 0o700); err != nil {
		return "", fmt.Errorf("home: make %s: %w", d, err)
	}
	return d, nil
}

// Path joins name under Dir (does not create).
func Path(name string) (string, error) {
	d, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(d, name), nil
}
