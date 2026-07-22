package codex

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sort"
	"sync"

	"skillctl/internal/model"
)

type Client struct {
	command string
	cwd     string
	cmd     *exec.Cmd
	stdin   io.WriteCloser
	decoder *json.Decoder
	encoder *json.Encoder
	stderr  bytes.Buffer
	nextID  int64
	mu      sync.Mutex
}

type rpcResponse struct {
	ID     json.RawMessage `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *rpcError       `json:"error"`
}

type rpcError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

type RPCError struct {
	Method  string
	Code    int
	Message string
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("%s failed (%d): %s", e.Method, e.Code, e.Message)
}

func IsMethodNotFound(err error) bool {
	var rpcErr *RPCError
	return errors.As(err, &rpcErr) && rpcErr.Code == -32601
}

func Open(ctx context.Context, command, cwd string) (*Client, error) {
	cmd := exec.CommandContext(ctx, command, "app-server", "--listen", "stdio://")
	cmd.Dir = cwd
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, err
	}
	client := &Client{
		command: command,
		cwd:     cwd,
		cmd:     cmd,
		stdin:   stdin,
		decoder: json.NewDecoder(bufio.NewReader(stdout)),
		encoder: json.NewEncoder(stdin),
		nextID:  1,
	}
	cmd.Stderr = &client.stderr
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start %s app-server: %w", command, err)
	}

	var initialized map[string]any
	if err := client.Call("initialize", map[string]any{
		"clientInfo": map[string]string{
			"name":    "skillctl",
			"version": "0.1.0",
		},
		"capabilities": map[string]bool{"experimentalApi": true},
	}, &initialized); err != nil {
		client.Close()
		return nil, err
	}
	if err := client.Notify("initialized", map[string]any{}); err != nil {
		client.Close()
		return nil, err
	}
	return client, nil
}

func (c *Client) Call(method string, params, result any) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	id := c.nextID
	c.nextID++
	request := map[string]any{"id": id, "method": method, "params": params}
	if err := c.encoder.Encode(request); err != nil {
		return fmt.Errorf("send %s request: %w", method, err)
	}
	for {
		var response rpcResponse
		if err := c.decoder.Decode(&response); err != nil {
			if errors.Is(err, io.EOF) && c.stderr.Len() > 0 {
				return fmt.Errorf("app-server closed during %s: %s", method, c.stderr.String())
			}
			return fmt.Errorf("read %s response: %w", method, err)
		}
		if len(response.ID) == 0 {
			continue
		}
		var responseID int64
		if err := json.Unmarshal(response.ID, &responseID); err != nil || responseID != id {
			continue
		}
		if response.Error != nil {
			return &RPCError{Method: method, Code: response.Error.Code, Message: response.Error.Message}
		}
		if result == nil || len(response.Result) == 0 {
			return nil
		}
		if err := json.Unmarshal(response.Result, result); err != nil {
			return fmt.Errorf("decode %s response: %w", method, err)
		}
		return nil
	}
}

func (c *Client) Notify(method string, params any) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.encoder.Encode(map[string]any{"method": method, "params": params})
}

func (c *Client) Close() error {
	if c == nil || c.cmd == nil {
		return nil
	}
	_ = c.stdin.Close()
	err := c.cmd.Wait()
	if err != nil && c.cmd.ProcessState != nil && c.cmd.ProcessState.ExitCode() != 0 {
		return err
	}
	return nil
}

type listResponse struct {
	Data []struct {
		CWD    string          `json:"cwd"`
		Skills []skillMetadata `json:"skills"`
		Errors []struct {
			Path    string `json:"path"`
			Message string `json:"message"`
		} `json:"errors"`
	} `json:"data"`
}

type skillMetadata struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	Path        string `json:"path"`
	Scope       string `json:"scope"`
	Enabled     bool   `json:"enabled"`
}

type skillConfigEntry struct {
	Path    string `json:"path"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
}

type configReadResponse struct {
	Config struct {
		Skills struct {
			Config []skillConfigEntry `json:"config"`
		} `json:"skills"`
	} `json:"config"`
}

type SkillEnablement struct {
	Names map[string]bool
	Paths map[string]bool
}

type InstalledPlugin struct {
	ID           string
	Marketplace  string
	Name         string
	Version      string
	LocalVersion string
	Installed    bool
	Enabled      bool
	SourceType   string
	SourcePath   string
}

type InstalledPlugins struct {
	bySource map[string]InstalledPlugin
	byID     map[string]InstalledPlugin
}

func NewInstalledPlugins(items ...InstalledPlugin) InstalledPlugins {
	plugins := InstalledPlugins{
		bySource: make(map[string]InstalledPlugin, len(items)),
		byID:     make(map[string]InstalledPlugin, len(items)),
	}
	for _, item := range items {
		plugins.bySource[item.Marketplace+":"+item.Name] = item
		plugins.byID[item.ID] = item
	}
	return plugins
}

func (p InstalledPlugins) LookupSource(source string) (InstalledPlugin, bool) {
	plugin, ok := p.bySource[source]
	return plugin, ok
}

func (p InstalledPlugins) LookupID(id string) (InstalledPlugin, bool) {
	plugin, ok := p.byID[id]
	return plugin, ok
}

func (p InstalledPlugins) Active() []InstalledPlugin {
	result := make([]InstalledPlugin, 0, len(p.byID))
	for _, plugin := range p.byID {
		if plugin.Installed && plugin.Enabled {
			result = append(result, plugin)
		}
	}
	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })
	return result
}

type installedPluginsResponse struct {
	Marketplaces []struct {
		Name    string `json:"name"`
		Plugins []struct {
			ID           string `json:"id"`
			Name         string `json:"name"`
			Version      string `json:"version"`
			LocalVersion string `json:"localVersion"`
			Installed    bool   `json:"installed"`
			Enabled      bool   `json:"enabled"`
			Source       struct {
				Type string `json:"type"`
				Path string `json:"path"`
			} `json:"source"`
		} `json:"plugins"`
	} `json:"marketplaces"`
	MarketplaceLoadErrors []struct {
		Path    string `json:"marketplacePath"`
		Message string `json:"message"`
	} `json:"marketplaceLoadErrors"`
}

func (c *Client) ListSkills(cwds []string) ([]skillMetadata, []string, error) {
	var response listResponse
	if err := c.Call("skills/list", map[string]any{
		"cwds":        cwds,
		"forceReload": true,
	}, &response); err != nil {
		return nil, nil, err
	}
	var skills []skillMetadata
	var warnings []string
	for _, entry := range response.Data {
		skills = append(skills, entry.Skills...)
		for _, item := range entry.Errors {
			warnings = append(warnings, fmt.Sprintf("%s: %s: %s", entry.CWD, item.Path, item.Message))
		}
	}
	return skills, warnings, nil
}

func (c *Client) DiscoverSkills(cwd string) ([]model.Skill, []string, error) {
	return Discover(c, cwd)
}

func (c *Client) ListInstalledPlugins(cwd string) (InstalledPlugins, []string, error) {
	var response installedPluginsResponse
	if err := c.Call("plugin/installed", map[string]any{
		"cwds": []string{cwd},
	}, &response); err != nil {
		return InstalledPlugins{}, nil, err
	}

	var installed []InstalledPlugin
	var warnings []string
	for _, marketplace := range response.Marketplaces {
		for _, item := range marketplace.Plugins {
			if item.ID == "" {
				warnings = append(warnings, fmt.Sprintf("marketplace %s returned a plugin without a canonical ID", marketplace.Name))
				continue
			}
			installed = append(installed, InstalledPlugin{
				ID:           item.ID,
				Marketplace:  marketplace.Name,
				Name:         item.Name,
				Version:      item.Version,
				LocalVersion: item.LocalVersion,
				Installed:    item.Installed,
				Enabled:      item.Enabled,
				SourceType:   item.Source.Type,
				SourcePath:   item.Source.Path,
			})
		}
	}
	for _, loadErr := range response.MarketplaceLoadErrors {
		warnings = append(warnings, fmt.Sprintf("marketplace %s: %s", loadErr.Path, loadErr.Message))
	}
	return NewInstalledPlugins(installed...), warnings, nil
}

func (c *Client) ReadSkillEnablement(cwd string) (SkillEnablement, error) {
	var response configReadResponse
	if err := c.Call("config/read", map[string]any{
		"cwd":           cwd,
		"includeLayers": false,
	}, &response); err != nil {
		return SkillEnablement{}, err
	}
	enablement := SkillEnablement{
		Names: map[string]bool{},
		Paths: map[string]bool{},
	}
	for _, entry := range response.Config.Skills.Config {
		if entry.Name != "" {
			enablement.Names[entry.Name] = entry.Enabled
		}
		if entry.Path != "" {
			enablement.Paths[entry.Path] = entry.Enabled
		}
	}
	return enablement, nil
}

func (c *Client) SetEnabled(path, name string, enabled bool) error {
	params := map[string]any{
		"path":    path,
		"name":    nil,
		"enabled": enabled,
	}
	if name != "" {
		params["path"] = nil
		params["name"] = name
	}
	return c.Call("skills/config/write", params, &map[string]any{})
}
