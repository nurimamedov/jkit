package jenkins

import "fmt"

type StageDiff struct {
	Name string `json:"name"`
	Path string `json:"path"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

func stageIndex(stages []Stage) (map[string]Stage, map[string]string, error) {
	byID := make(map[string]Stage, len(stages))
	for _, stage := range stages {
		if _, exists := byID[stage.ID]; exists || stage.ID == "" {
			return nil, nil, fmt.Errorf("missing or duplicate stage ID %q", stage.ID)
		}
		byID[stage.ID] = stage
	}
	// Validate ancestry before QualifiedStagePaths follows parent links.
	finished := make(map[string]bool, len(stages))
	for _, stage := range stages {
		seen := make(map[string]bool)
		for id := stage.ID; id != "" && !finished[id]; {
			if seen[id] {
				return nil, nil, fmt.Errorf("cyclic stage ancestry at %q", id)
			}
			seen[id] = true
			parent, exists := byID[id]
			if !exists {
				break // Blue Ocean can omit non-stage ancestor nodes.
			}
			id = parent.FirstParent
		}
		for id := range seen {
			finished[id] = true
		}
	}
	paths := QualifiedStagePaths(stages)
	byPath := make(map[string]Stage, len(stages))
	for _, stage := range stages {
		path := paths[stage.ID]
		if _, exists := byPath[path]; exists {
			return nil, nil, fmt.Errorf("ambiguous stage path %q", path)
		}
		byPath[path] = stage
	}
	return byPath, paths, nil
}

// CompareStages matches qualified paths across builds; node IDs are build-local.
func CompareStages(before, after []Stage) ([]StageDiff, error) {
	old, oldPaths, err := stageIndex(before)
	if err != nil {
		return nil, err
	}
	current, paths, err := stageIndex(after)
	if err != nil {
		return nil, err
	}
	var diffs []StageDiff
	for _, stage := range after {
		path := paths[stage.ID]
		previous, exists := old[path]
		if !exists || previous.Status != stage.Status {
			diffs = append(diffs, StageDiff{stage.Name, path, previous.Status, stage.Status})
		}
	}
	for _, stage := range before {
		path := oldPaths[stage.ID]
		if _, exists := current[path]; !exists {
			diffs = append(diffs, StageDiff{stage.Name, path, stage.Status, "(removed)"})
		}
	}
	return diffs, nil
}
