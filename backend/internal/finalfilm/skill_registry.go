package finalfilm

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const directorSkillRuntimeSchemaVersion = "demoops.director_skill_runtime.v1"

var requiredDirectorSkills = []string{"director-evidence-story", "director-generated-shots", "director-timeline-compose", "director-quality-gate", "final-film-director-harness"}

type DirectorSkillRuntime struct {
	SchemaVersion string         `json:"schema_version"`
	SkillID       string         `json:"skill_id"`
	Version       string         `json:"version"`
	Inputs        []string       `json:"inputs"`
	Outputs       []string       `json:"outputs"`
	References    []string       `json:"references"`
	Configuration map[string]any `json:"-"`
}

func LoadDirectorSkillRuntimes(root string) (map[string]DirectorSkillRuntime, error) {
	root = filepath.Clean(strings.TrimSpace(root))
	if root == "." || root == "" {
		return nil, errors.New("Director skill root is required")
	}
	result := make(map[string]DirectorSkillRuntime, len(requiredDirectorSkills))
	for _, skillID := range requiredDirectorSkills {
		directory := filepath.Join(root, skillID)
		payload, err := os.ReadFile(filepath.Join(directory, "agents", "runtime.json"))
		if err != nil {
			return nil, fmt.Errorf("%s runtime: %w", skillID, err)
		}
		var runtime DirectorSkillRuntime
		if err := json.Unmarshal(payload, &runtime); err != nil {
			return nil, fmt.Errorf("%s runtime: %w", skillID, err)
		}
		if runtime.SchemaVersion != directorSkillRuntimeSchemaVersion || runtime.SkillID != skillID || runtime.Version == "" || len(runtime.Inputs) == 0 || len(runtime.Outputs) == 0 {
			return nil, fmt.Errorf("%s runtime identity or IO is invalid", skillID)
		}
		lower := strings.ToLower(string(payload))
		for _, forbidden := range []string{"cascadeai" + ".cn", "tet" + "ris", "俄罗斯" + "方块", "data-" + "testid", "preview-" + "iframe"} {
			if strings.Contains(lower, forbidden) {
				return nil, fmt.Errorf("%s runtime contains site-specific token %s", skillID, forbidden)
			}
		}
		for _, reference := range runtime.References {
			path := filepath.Clean(filepath.Join(directory, filepath.FromSlash(reference)))
			if !strings.HasPrefix(strings.ToLower(path), strings.ToLower(filepath.Clean(directory)+string(filepath.Separator))) {
				return nil, fmt.Errorf("%s reference escapes its skill directory", skillID)
			}
			if info, err := os.Stat(path); err != nil || !info.Mode().IsRegular() {
				return nil, fmt.Errorf("%s reference %s is unavailable", skillID, reference)
			}
		}
		result[skillID] = runtime
	}
	return result, nil
}
