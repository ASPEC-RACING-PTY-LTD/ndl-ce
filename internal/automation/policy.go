package automation

import (
	"fmt"
	"strings"

	"gopkg.in/yaml.v3"
)

const (
	KindStoragePressure  = "storage_pressure"
	ActionEnqueueMigrate = "enqueue_migrate_low_priority"
	ApplyConfirm         = "apply-policy"
	ActorName            = "nodal-automation"
)

var prohibitedKeys = []string{
	"run", "script", "bash", "exec", "helper", "host_exec", "command", "shell", "argv",
}

// Spec is a deterministic policy. It is not an LLM prompt and not a shell.
type Spec struct {
	Kind             string `yaml:"kind" json:"kind"`
	ThresholdPercent int    `yaml:"threshold_percent" json:"threshold_percent"`
	Action           string `yaml:"action" json:"action"`
	RequireApproval  bool   `yaml:"require_approval" json:"require_approval"`
}

// ParseYAML loads a policy spec and rejects Host.Exec-shaped keys.
func ParseYAML(raw []byte) (*Spec, error) {
	if err := rejectProhibited(raw); err != nil {
		return nil, err
	}
	var spec Spec
	if err := yaml.Unmarshal(raw, &spec); err != nil {
		return nil, fmt.Errorf("policy yaml is invalid")
	}
	return Normalize(spec)
}

// ParseJSONMap validates a JSON object body from the API.
func ParseJSONMap(kind, action string, threshold int, requireApproval bool) (*Spec, error) {
	return Normalize(Spec{
		Kind: kind, Action: action, ThresholdPercent: threshold, RequireApproval: requireApproval,
	})
}

// Normalize validates typed fields. Unknown actions fail closed.
func Normalize(spec Spec) (*Spec, error) {
	spec.Kind = strings.TrimSpace(spec.Kind)
	spec.Action = strings.TrimSpace(spec.Action)
	if spec.Kind == "" {
		spec.Kind = KindStoragePressure
	}
	if spec.Action == "" {
		spec.Action = ActionEnqueueMigrate
	}
	if spec.Kind != KindStoragePressure {
		return nil, fmt.Errorf("policy kind is unsupported")
	}
	if spec.Action != ActionEnqueueMigrate {
		return nil, fmt.Errorf("policy action is unsupported")
	}
	if spec.ThresholdPercent == 0 {
		spec.ThresholdPercent = 85
	}
	if spec.ThresholdPercent < 1 || spec.ThresholdPercent > 100 {
		return nil, fmt.Errorf("threshold_percent must be 1 to 100")
	}
	return &spec, nil
}

func rejectProhibited(raw []byte) error {
	var node yaml.Node
	if err := yaml.Unmarshal(raw, &node); err != nil {
		return fmt.Errorf("policy yaml is invalid")
	}
	return walkYAML(&node)
}

func walkYAML(n *yaml.Node) error {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.MappingNode {
		for i := 0; i+1 < len(n.Content); i += 2 {
			key := strings.ToLower(strings.TrimSpace(n.Content[i].Value))
			for _, banned := range prohibitedKeys {
				if key == banned {
					return fmt.Errorf("policy must not contain %s", banned)
				}
			}
			if err := walkYAML(n.Content[i+1]); err != nil {
				return err
			}
		}
		return nil
	}
	for _, c := range n.Content {
		if err := walkYAML(c); err != nil {
			return err
		}
	}
	return nil
}
