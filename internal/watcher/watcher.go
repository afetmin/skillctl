package watcher

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"skillctl/internal/agent"
	"skillctl/internal/claude"
	"skillctl/internal/codex"
	"skillctl/internal/config"
	"skillctl/internal/model"
	"skillctl/internal/skillfs"
	statestore "skillctl/internal/state"
)

type Watcher struct {
	ConfigPath  string
	StateDir    string
	RuntimePath string
	CWD         string
	Interval    time.Duration
	Project     bool
}

func (w Watcher) Run(ctx context.Context, sync func(context.Context, model.Agent) error) error {
	if w.Interval <= 0 {
		w.Interval = 5 * time.Second
	}
	var target model.Agent
	var last string
	syncTarget := func(next model.Agent) error {
		target = next
		if err := sync(ctx, target); err != nil {
			last = ""
			return nil
		}
		var err error
		last, err = w.Fingerprint(target)
		return err
	}
	runCycle := func(force bool) error {
		return w.withRuntimeLock(func() error {
			next, err := w.Target()
			if err != nil {
				return err
			}
			current, err := w.Fingerprint(next)
			if err != nil {
				return err
			}
			if !force && next == target && current == last {
				return nil
			}
			return syncTarget(next)
		})
	}
	runTargetCycle := func() error {
		return w.withRuntimeLock(func() error {
			next, err := w.Target()
			if err != nil || next == target {
				return err
			}
			return syncTarget(next)
		})
	}
	if err := runCycle(true); err != nil {
		return err
	}
	ticker := time.NewTicker(w.Interval)
	defer ticker.Stop()
	targetTicker := time.NewTicker(100 * time.Millisecond)
	defer targetTicker.Stop()
	for {
		select {
		case <-ctx.Done():
			return nil
		case <-targetTicker.C:
			if err := runTargetCycle(); err != nil {
				return err
			}
		case <-ticker.C:
			if err := runCycle(false); err != nil {
				return err
			}
		}
	}
}

func (w Watcher) Target() (model.Agent, error) {
	runtimePath := w.runtimePath()
	value, err := statestore.LoadRuntime(runtimePath)
	if err != nil {
		return "", err
	}
	if value.WatcherAgent.Valid() {
		return value.WatcherAgent, nil
	}
	cfg, _, err := config.LoadOrDefault(w.ConfigPath)
	if err != nil {
		return "", err
	}
	selected, err := agent.Detect(cfg, "")
	if err != nil {
		return "", err
	}
	value.WatcherAgent = selected
	if err := statestore.SaveRuntime(runtimePath, value); err != nil {
		return "", err
	}
	return selected, nil
}

func (w Watcher) SetTarget(target model.Agent) error {
	if !target.Valid() {
		return fmt.Errorf("invalid watcher Agent %q", target)
	}
	return w.withRuntimeLock(func() error {
		path := w.runtimePath()
		value, err := statestore.LoadRuntime(path)
		if err != nil {
			return err
		}
		value.WatcherAgent = target
		return statestore.SaveRuntime(path, value)
	})
}

func (w Watcher) Fingerprint(target model.Agent) (string, error) {
	if !target.Valid() {
		return "", fmt.Errorf("invalid watcher Agent %q", target)
	}
	var roots []skillfs.Root
	var files []string
	var err error
	switch target {
	case model.AgentCodex:
		roots, err = codex.SupportedRoots(w.CWD)
		if err != nil {
			return "", err
		}
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		files = append(files, filepath.Join(home, ".codex", "config.toml"))
	case model.AgentClaude:
		roots, err = claude.Roots(w.CWD)
		if err != nil {
			return "", err
		}
		paths, err := claude.Paths(w.CWD)
		if err != nil {
			return "", err
		}
		files = append(files, paths.User, paths.Shared, paths.Local)
		files = append(files, paths.Managed...)
	}
	var records []string
	for _, root := range roots {
		if root.Scope == model.ScopeRepo && !w.Project {
			continue
		}
		entries, readErr := os.ReadDir(root.Path)
		if readErr != nil && !os.IsNotExist(readErr) {
			return "", readErr
		}
		for _, entry := range entries {
			if target == model.AgentCodex && entry.Name() == ".system" {
				continue
			}
			skillPath := filepath.Join(root.Path, entry.Name(), "SKILL.md")
			if info, statErr := os.Stat(skillPath); statErr == nil && !info.IsDir() {
				records = append(records, fileRecord(skillPath, info))
			} else if statErr != nil && !os.IsNotExist(statErr) {
				return "", statErr
			}
			if target != model.AgentCodex {
				continue
			}
			policyPath := filepath.Join(root.Path, entry.Name(), "agents", "openai.yaml")
			if info, statErr := os.Stat(policyPath); statErr == nil && !info.IsDir() {
				records = append(records, fileRecord(policyPath, info))
			} else if statErr != nil && !os.IsNotExist(statErr) {
				return "", statErr
			}
		}
	}
	files = append(files, w.ConfigPath, statestore.Path(w.StateDir, target), w.runtimePath())
	for _, path := range files {
		if path == "" {
			continue
		}
		if info, err := os.Stat(path); err == nil {
			records = append(records, fileRecord(path, info))
		} else if !os.IsNotExist(err) {
			return "", err
		}
	}
	sort.Strings(records)
	sum := sha256.Sum256([]byte(strings.Join(records, "\n")))
	return hex.EncodeToString(sum[:]), nil
}

func (w Watcher) runtimePath() string {
	if w.RuntimePath != "" {
		return w.RuntimePath
	}
	return statestore.RuntimePath(w.StateDir)
}

func (w Watcher) withRuntimeLock(run func() error) error {
	path := w.runtimePath() + ".lock"
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	defer file.Close()
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		return err
	}
	defer syscall.Flock(int(file.Fd()), syscall.LOCK_UN)
	return run()
}

func fileRecord(path string, info os.FileInfo) string {
	return fmt.Sprintf("%s|%d|%d", path, info.Size(), info.ModTime().UnixNano())
}
