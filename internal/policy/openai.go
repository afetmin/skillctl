package policy

import (
	"bytes"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"gopkg.in/yaml.v3"

	"skillctl/internal/fileutil"
)

type Snapshot struct {
	FileExisted bool
	Present     bool
	Value       bool
}

func Inspect(path string) (Snapshot, error) {
	root, existed, err := load(path)
	if err != nil {
		return Snapshot{}, err
	}
	value, present, err := policyValue(root)
	if err != nil {
		return Snapshot{}, err
	}
	return Snapshot{FileExisted: existed, Present: present, Value: value}, nil
}

func Read(path string) (*bool, error) {
	snapshot, err := Inspect(path)
	if err != nil {
		return nil, err
	}
	if !snapshot.Present {
		return nil, nil
	}
	value := snapshot.Value
	return &value, nil
}

func Set(path string, allow bool) (string, error) {
	root, _, err := load(path)
	if err != nil {
		return "", err
	}
	mapping, err := rootMapping(root)
	if err != nil {
		return "", err
	}
	policyNode := mapValue(mapping, "policy")
	if policyNode == nil {
		policyNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
		appendMapValue(mapping, "policy", policyNode)
	}
	if policyNode.Kind != yaml.MappingNode {
		return "", errors.New("agents/openai.yaml policy must be a mapping")
	}
	setMapScalar(policyNode, "allow_implicit_invocation", allow)
	if err := write(path, root); err != nil {
		return "", err
	}
	return fileutil.HashFile(path)
}

func Restore(path string, snapshot Snapshot) (string, error) {
	root, existed, err := load(path)
	if err != nil {
		return "", err
	}
	if !existed && !snapshot.FileExisted {
		return "", nil
	}
	mapping, err := rootMapping(root)
	if err != nil {
		return "", err
	}
	policyNode := mapValue(mapping, "policy")
	if snapshot.Present {
		if policyNode == nil {
			policyNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			appendMapValue(mapping, "policy", policyNode)
		}
		if policyNode.Kind != yaml.MappingNode {
			return "", errors.New("agents/openai.yaml policy must be a mapping")
		}
		setMapScalar(policyNode, "allow_implicit_invocation", snapshot.Value)
	} else if policyNode != nil {
		if policyNode.Kind != yaml.MappingNode {
			return "", errors.New("agents/openai.yaml policy must be a mapping")
		}
		removeMapValue(policyNode, "allow_implicit_invocation")
		if len(policyNode.Content) == 0 {
			removeMapValue(mapping, "policy")
		}
	}

	if !snapshot.FileExisted && len(mapping.Content) == 0 {
		if err := os.Remove(path); err != nil && !errors.Is(err, os.ErrNotExist) {
			return "", err
		}
		_ = os.Remove(filepath.Dir(path))
		return "", nil
	}
	if err := write(path, root); err != nil {
		return "", err
	}
	return fileutil.HashFile(path)
}

func load(path string) (*yaml.Node, bool, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return newDocument(), false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return newDocument(), true, nil
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return nil, true, fmt.Errorf("parse %s: %w", path, err)
	}
	return &root, true, nil
}

func newDocument() *yaml.Node {
	return &yaml.Node{
		Kind:    yaml.DocumentNode,
		Content: []*yaml.Node{{Kind: yaml.MappingNode, Tag: "!!map"}},
	}
}

func rootMapping(root *yaml.Node) (*yaml.Node, error) {
	if root.Kind != yaml.DocumentNode || len(root.Content) != 1 {
		return nil, errors.New("agents/openai.yaml must contain one YAML document")
	}
	mapping := root.Content[0]
	if mapping.Kind != yaml.MappingNode {
		return nil, errors.New("agents/openai.yaml root must be a mapping")
	}
	return mapping, nil
}

func policyValue(root *yaml.Node) (bool, bool, error) {
	mapping, err := rootMapping(root)
	if err != nil {
		return false, false, err
	}
	policyNode := mapValue(mapping, "policy")
	if policyNode == nil {
		return false, false, nil
	}
	if policyNode.Kind != yaml.MappingNode {
		return false, false, errors.New("agents/openai.yaml policy must be a mapping")
	}
	value := mapValue(policyNode, "allow_implicit_invocation")
	if value == nil {
		return false, false, nil
	}
	var allow bool
	if err := value.Decode(&allow); err != nil {
		return false, false, errors.New("policy.allow_implicit_invocation must be a boolean")
	}
	return allow, true, nil
}

func mapValue(mapping *yaml.Node, key string) *yaml.Node {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			return mapping.Content[index+1]
		}
	}
	return nil
}

func appendMapValue(mapping *yaml.Node, key string, value *yaml.Node) {
	mapping.Content = append(mapping.Content,
		&yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: key},
		value,
	)
}

func setMapScalar(mapping *yaml.Node, key string, value bool) {
	node := mapValue(mapping, key)
	if node == nil {
		node = &yaml.Node{}
		appendMapValue(mapping, key, node)
	}
	node.Kind = yaml.ScalarNode
	node.Tag = "!!bool"
	if value {
		node.Value = "true"
	} else {
		node.Value = "false"
	}
}

func removeMapValue(mapping *yaml.Node, key string) {
	for index := 0; index+1 < len(mapping.Content); index += 2 {
		if mapping.Content[index].Value == key {
			mapping.Content = append(mapping.Content[:index], mapping.Content[index+2:]...)
			return
		}
	}
}

func write(path string, root *yaml.Node) error {
	var buffer bytes.Buffer
	encoder := yaml.NewEncoder(&buffer)
	encoder.SetIndent(2)
	if err := encoder.Encode(root); err != nil {
		return err
	}
	if err := encoder.Close(); err != nil {
		return err
	}
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	} else if !errors.Is(err, os.ErrNotExist) {
		return err
	}
	return fileutil.WriteAtomic(path, buffer.Bytes(), mode)
}
