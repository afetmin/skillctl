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
	"sync"
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
	Paths map[string]bool
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
		Paths: map[string]bool{},
	}
	for _, entry := range response.Config.Skills.Config {
		if entry.Path != "" {
			enablement.Paths[entry.Path] = entry.Enabled
		}
	}
	return enablement, nil
}

func (c *Client) SetEnabled(path string, enabled bool) error {
	params := map[string]any{
		"path":    path,
		"name":    nil,
		"enabled": enabled,
	}
	return c.Call("skills/config/write", params, &map[string]any{})
}
