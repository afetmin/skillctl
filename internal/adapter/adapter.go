package adapter

import (
	"context"
	"fmt"

	"skillctl/internal/model"
	statestore "skillctl/internal/state"
)

type Adapter interface {
	Agent() model.Agent
	States() []model.InvocationState
	Discover(context.Context) ([]model.Skill, model.DiscoveryReport, error)
	NeedsApply(model.Skill, model.InvocationState) (bool, error)
	Prepare(model.Skill, model.InvocationState, *statestore.Entry) (statestore.Entry, error)
	Apply(model.Skill, model.InvocationState, statestore.Entry) (statestore.Entry, error)
	CheckRestore(statestore.Entry) error
	Restore(statestore.Entry) error
	Delete(model.Skill) error
	Close() error
}

func New(agent model.Agent, command, cwd string) (Adapter, error) {
	switch agent {
	case model.AgentCodex:
		return newCodex(command, cwd), nil
	case model.AgentClaude:
		return newClaude(cwd), nil
	default:
		return nil, fmt.Errorf("unsupported agent %q", agent)
	}
}
