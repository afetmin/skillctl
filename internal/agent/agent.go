package agent

import (
	"fmt"
	"os/exec"
	"strings"

	"skillctl/internal/config"
	"skillctl/internal/model"
)

var priority = []model.Agent{model.AgentCodex, model.AgentClaude}

func Parse(value string) (model.Agent, error) {
	agent := model.Agent(strings.ToLower(strings.TrimSpace(value)))
	if !agent.Valid() {
		return "", fmt.Errorf("unsupported agent %q; expected codex or claude", value)
	}
	return agent, nil
}

func Detect(cfg config.Config, explicit string) (model.Agent, error) {
	if strings.TrimSpace(explicit) != "" {
		agent, err := Parse(explicit)
		if err != nil {
			return "", err
		}
		value, err := cfg.Agent(agent)
		if err != nil {
			return "", err
		}
		if _, err := exec.LookPath(value.Command); err != nil {
			return "", fmt.Errorf("%s command %q was not found in PATH", agent, value.Command)
		}
		return agent, nil
	}
	available := Available(cfg)
	if len(available) == 0 {
		return "", fmt.Errorf("no supported Agent command was found in PATH; install Codex or Claude, or configure agents.<name>.command")
	}
	return available[0], nil
}

func Available(cfg config.Config) []model.Agent {
	result := make([]model.Agent, 0, len(priority))
	for _, agent := range priority {
		value, err := cfg.Agent(agent)
		if err != nil || strings.TrimSpace(value.Command) == "" {
			continue
		}
		if _, err := exec.LookPath(value.Command); err == nil {
			result = append(result, agent)
		}
	}
	return result
}
