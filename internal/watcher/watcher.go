package watcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Watcher struct {
	ConfigPath string
	Interval   time.Duration
}

func (w Watcher) Run(ctx context.Context, sync func(context.Context) error) error {
	if w.Interval <= 0 {
		w.Interval = 5 * time.Second
	}
	if err := sync(ctx); err != nil {
		return err
	}
	last, err := w.fingerprint()
	if err != nil {
		return err
	}
	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
			current, err := w.fingerprint()
			if err != nil {
				return err
			}
			if current == last {
				continue
			}
			if err := sync(ctx); err != nil {
				return err
			}
			last, err = w.fingerprint()
			if err != nil {
				return err
			}
		}
	}
}

func (w Watcher) fingerprint() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	roots := []string{
		filepath.Join(home, ".agents", "skills"),
		filepath.Join(home, ".codex", "skills"),
		filepath.Join(home, ".codex", "plugins", "cache"),
	}
	var records []string
	for _, root := range roots {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, walkErr error) error {
			if walkErr != nil {
				if os.IsNotExist(walkErr) {
					return nil
				}
				return walkErr
			}
			if entry.IsDir() {
				return nil
			}
			clean := filepath.ToSlash(path)
			if entry.Name() != "SKILL.md" && !strings.HasSuffix(clean, "/agents/openai.yaml") {
				return nil
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			records = append(records, fmt.Sprintf("%s|%d|%d", path, info.Size(), info.ModTime().UnixNano()))
			return nil
		})
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
	}
	if info, err := os.Stat(w.ConfigPath); err == nil {
		records = append(records, fmt.Sprintf("%s|%d|%d", w.ConfigPath, info.Size(), info.ModTime().UnixNano()))
	} else if !os.IsNotExist(err) {
		return "", err
	}
	sort.Strings(records)
	sum := sha256.Sum256([]byte(strings.Join(records, "\n")))
	return hex.EncodeToString(sum[:]), nil
}
