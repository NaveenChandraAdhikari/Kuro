package storage

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
)

type Paths struct {
	Data   string
	Config string
	Cache  string
}

func DefaultPaths() (Paths, error) {
	data, err := defaultDataDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve user data directory: %w", err)
	}
	config, err := os.UserConfigDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve config directory: %w", err)
	}
	cache, err := os.UserCacheDir()
	if err != nil {
		return Paths{}, fmt.Errorf("resolve cache directory: %w", err)
	}

	paths := Paths{
		Data:   filepath.Join(data, "kuro"),
		Config: filepath.Join(config, "kuro"),
		Cache:  filepath.Join(cache, "kuro"),
	}
	if value := os.Getenv("KURO_DATA_DIR"); value != "" {
		paths.Data = value
	}
	if value := os.Getenv("KURO_CONFIG_DIR"); value != "" {
		paths.Config = value
	}
	if value := os.Getenv("KURO_CACHE_DIR"); value != "" {
		paths.Cache = value
	}
	return paths, nil
}

func defaultDataDir() (string, error) {
	if value := os.Getenv("XDG_DATA_HOME"); value != "" && runtime.GOOS != "windows" {
		return value, nil
	}
	if runtime.GOOS == "darwin" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		return filepath.Join(home, "Library", "Application Support"), nil
	}
	if runtime.GOOS == "windows" {
		if value := os.Getenv("APPDATA"); value != "" {
			return value, nil
		}
		return os.UserConfigDir()
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".local", "share"), nil
}

func (p Paths) EntriesDir() string { return filepath.Join(p.Data, "entries") }

func (p Paths) Ensure() error {
	for _, path := range []string{p.Data, p.Config, p.Cache, p.EntriesDir()} {
		if err := os.MkdirAll(path, 0o700); err != nil {
			return fmt.Errorf("create %s: %w", path, err)
		}
	}
	return nil
}
