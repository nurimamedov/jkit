package jenkins

import (
	"errors"
	"fmt"
	"strings"
)

// MaxStageGraphNodes is a client defensive limit, not a Jenkins capacity limit.
const MaxStageGraphNodes = 10000

// MaxStageGraphExpansionBytes bounds qualified paths plus cached prefixes,
// the padded qualified-path column, or indented name/status display columns. It is a client memory/output policy, not a
// Jenkins limit. Check before allocating expanded strings or rendering a tree.
const MaxStageGraphExpansionBytes = 8 << 20

// ErrInvalidStageGraph identifies malformed or excessive remote stage metadata.
var ErrInvalidStageGraph = errors.New("invalid stage graph")

// ValidateStageGraph rejects ambiguous IDs and cyclic ancestry. Missing parent
// nodes are allowed: Blue Ocean may omit non-stage ancestors from its response.
func ValidateStageGraph(stages []Stage) error {
	_, err := validatedStageIDs(stages)
	return err
}

func validatedStageIDs(stages []Stage) (map[string]Stage, error) {
	if len(stages) > MaxStageGraphNodes {
		return nil, fmt.Errorf("%w: exceeds client limit of %d nodes", ErrInvalidStageGraph, MaxStageGraphNodes)
	}
	byID := make(map[string]Stage, len(stages))
	for _, stage := range stages {
		if _, exists := byID[stage.ID]; exists || stage.ID == "" {
			return nil, fmt.Errorf("%w: missing or duplicate stage ID %q", ErrInvalidStageGraph, stage.ID)
		}
		byID[stage.ID] = stage
	}
	// Each node enters and leaves the active ancestry once, so validation is
	// linear even when the response lists descendants before their parents.
	state := make(map[string]uint8, len(stages))
	for _, stage := range stages {
		var chain []string
		for id := stage.ID; id != "" && state[id] != 2; {
			if state[id] == 1 {
				return nil, fmt.Errorf("%w: cyclic stage ancestry at %q", ErrInvalidStageGraph, id)
			}
			parent, exists := byID[id]
			if !exists {
				break
			}
			state[id] = 1
			chain = append(chain, id)
			id = parent.FirstParent
		}
		for _, id := range chain {
			state[id] = 2
		}
	}
	return byID, nil
}

// NonContainerStages filters out fan-out stages (parallel containers) that have
// multiple children. Sequential predecessors (single child) are kept since they
// are real stages with their own logs. For flat pipelines, all stages returned.
func NonContainerStages(stages []Stage) []Stage {
	// Count children per parent.
	childCount := make(map[string]int)
	for _, s := range stages {
		if s.FirstParent != "" {
			childCount[s.FirstParent]++
		}
	}
	if len(childCount) == 0 {
		return stages
	}
	out := make([]Stage, 0, len(stages))
	for _, s := range stages {
		if childCount[s.ID] <= 1 {
			out = append(out, s)
		}
	}
	return out
}

// QualifiedStagePaths maps each stage ID to a human-readable path that
// disambiguates duplicate stage names, e.g. "RemoteExec/Run Bazel Build".
// Segments are the names of the enclosing parallel-branch heads (outermost
// first) followed by the stage's own name. Stages with no parallel ancestor
// map to their bare name.
//
// FirstParent is overloaded: it links both sequential predecessors and
// hierarchical parents (see pgv.go). Only branch heads — stages whose
// FirstParent is a fan-out node (a node with >1 children) — contribute a path
// segment, so flat sequential pipelines yield bare names while parallel
// branches get a qualifying prefix.
func QualifiedStagePaths(stages []Stage) (map[string]string, error) {
	byID, err := validatedStageIDs(stages)
	if err != nil {
		return nil, err
	}
	childCount := make(map[string]int, len(stages))
	for _, s := range stages {
		if s.FirstParent != "" {
			childCount[s.FirstParent]++
		}
	}

	paths := make(map[string]string, len(stages))
	// Cache the enclosing branch prefix passed to each node's descendants.
	// Sequential chains then require one visit per node, not one ancestry walk
	// per stage. The input may list descendants before their parents.
	prefixes := make(map[string]string, len(stages))
	expandedBytes := 0
	for _, s := range stages {
		var chain []Stage
		id := s.ID
		for id != "" {
			if _, done := prefixes[id]; done {
				break
			}
			ancestor, exists := byID[id]
			if !exists {
				break
			}
			chain = append(chain, ancestor)
			id = ancestor.FirstParent
		}
		prefix := prefixes[id]
		for i := len(chain) - 1; i >= 0; i-- {
			ancestor := chain[i]
			branchHead := childCount[ancestor.FirstParent] > 1 && ancestor.Name != ""
			remaining := MaxStageGraphExpansionBytes - expandedBytes
			if len(prefix) > remaining || len(ancestor.Name) > remaining-len(prefix) {
				return nil, fmt.Errorf("%w: qualified paths exceed client expansion limit of %d bytes", ErrInvalidStageGraph, MaxStageGraphExpansionBytes)
			}
			pathBytes := len(prefix) + len(ancestor.Name)
			if pathBytes > MaxStageGraphExpansionBytes/len(stages) {
				return nil, fmt.Errorf("%w: qualified path display exceeds client expansion limit of %d bytes", ErrInvalidStageGraph, MaxStageGraphExpansionBytes)
			}
			prefixBytes := len(prefix)
			if branchHead {
				prefixBytes = pathBytes + 1
			}
			if prefixBytes > remaining-pathBytes {
				return nil, fmt.Errorf("%w: qualified paths exceed client expansion limit of %d bytes", ErrInvalidStageGraph, MaxStageGraphExpansionBytes)
			}
			expandedBytes += pathBytes + prefixBytes
			paths[ancestor.ID] = prefix + ancestor.Name
			if branchHead {
				prefix += ancestor.Name + "/"
			}
			prefixes[ancestor.ID] = prefix
		}
	}
	return paths, nil
}

// FlatStage is a Stage with a nesting depth for display purposes.
type FlatStage struct {
	Stage
	Depth int
}

// BuildStageTree converts a flat list of stages (as returned by the Blue Ocean
// API) into a depth-annotated list suitable for indented rendering. Parent-child
// relationships are derived from Stage.FirstParent. If no stage has a parent,
// all stages are returned at depth 0 (backward compatible with older Jenkins).
func BuildStageTree(stages []Stage) ([]FlatStage, error) {
	if err := ValidateStageGraph(stages); err != nil {
		return nil, err
	}
	if len(stages) == 0 {
		return nil, nil
	}

	ids := make(map[string]struct{}, len(stages))
	for _, s := range stages {
		ids[s.ID] = struct{}{}
	}

	// Group children by parent ID, preserving original order.
	children := make(map[string][]int) // parentID -> indices into stages
	var roots []int
	for i, s := range stages {
		if s.FirstParent == "" {
			roots = append(roots, i)
		} else if _, ok := ids[s.FirstParent]; !ok {
			// Orphan: parent not in list, treat as root
			roots = append(roots, i)
		} else {
			children[s.FirstParent] = append(children[s.FirstParent], i)
		}
	}

	// DFS walk emitting each node with its depth.
	// Blue Ocean chains ALL stages via firstParent — sequential and parallel.
	// We indent at two points:
	//   1. Fan-out (multiple children, i.e. parallel) → children at depth+1
	//   2. Branch header (direct child of fan-out) → its successor at depth+1
	// After that, single-child chains stay flat (same depth).
	out := make([]FlatStage, 0, len(stages))
	maxName, maxStatus := 0, 0
	var walk func(idx, depth int, isBranchHead bool) error
	walk = func(idx, depth int, isBranchHead bool) error {
		// Text rendering pads every row to the widest indented name/status.
		// Account for that multiplication without constructing padded strings.
		maxName = max(maxName, len(stages[idx].Name)+2*depth)
		maxStatus = max(maxStatus, len(stages[idx].Status))
		if maxName+maxStatus > MaxStageGraphExpansionBytes/len(stages) {
			return fmt.Errorf("%w: stage display exceeds client expansion limit of %d bytes", ErrInvalidStageGraph, MaxStageGraphExpansionBytes)
		}
		out = append(out, FlatStage{Stage: stages[idx], Depth: depth})
		kids := children[stages[idx].ID]
		if len(kids) > 1 {
			for _, ci := range kids {
				if err := walk(ci, depth+1, true); err != nil {
					return err
				}
			}
		} else if len(kids) == 1 {
			if isBranchHead {
				return walk(kids[0], depth+1, false)
			} else {
				return walk(kids[0], depth, false)
			}
		}
		return nil
	}
	for _, ri := range roots {
		if err := walk(ri, 0, false); err != nil {
			return nil, err
		}
	}
	return out, nil
}

// ResolveStageID maps a user-supplied stage name, qualified path or node ID to
// a unique node ID. It matches qualified paths first (e.g. "RemoteExec/Run
// Bazel Build"), then bare stage names, then exact IDs, so a stage literally
// named "3366" wins over the stage with ID 3366. An ambiguous bare name returns an error listing every
// candidate's qualified path and ID so the caller can pick one.
func ResolveStageID(stages []Stage, input string) (string, error) {
	paths, err := QualifiedStagePaths(stages)
	if err != nil {
		return "", err
	}

	var pathMatches, nameMatches []Stage
	for _, s := range stages {
		if strings.EqualFold(paths[s.ID], input) {
			pathMatches = append(pathMatches, s)
		}
		if strings.EqualFold(s.Name, input) {
			nameMatches = append(nameMatches, s)
		}
	}

	if len(pathMatches) == 1 {
		return pathMatches[0].ID, nil
	}
	if len(pathMatches) == 0 && len(nameMatches) == 1 {
		return nameMatches[0].ID, nil
	}

	if len(pathMatches) == 0 && len(nameMatches) == 0 {
		for _, s := range stages {
			if s.ID == input {
				return s.ID, nil
			}
		}
	}

	// Determine candidate set for messaging.
	candidates := pathMatches
	if len(candidates) == 0 {
		candidates = nameMatches
	}
	if len(candidates) == 0 {
		return "", stageNotFound(stages, paths, input)
	}

	matches := make([]string, 0, len(candidates))
	for _, s := range candidates {
		status := s.Status
		if status == "" {
			status = "?"
		}
		matches = append(matches, fmt.Sprintf("%s  (id=%s, %s)", paths[s.ID], s.ID, status))
	}
	return "", &StageAmbiguousError{Input: input, Matches: matches, Example: paths[candidates[0].ID]}
}

// RequireStageID returns a StageNotFoundError unless a stage has node ID id.
func RequireStageID(stages []Stage, id string) error {
	for _, s := range stages {
		if s.ID == id {
			return nil
		}
	}
	paths, err := QualifiedStagePaths(stages)
	if err != nil {
		return err
	}
	nf := stageNotFound(stages, paths, id)
	nf.ByID = true
	return nf
}

func stageNotFound(stages []Stage, paths map[string]string, input string) *StageNotFoundError {
	available := make([]string, 0, len(stages))
	for _, s := range stages {
		available = append(available, fmt.Sprintf("%s (%s)", paths[s.ID], s.ID))
	}
	return &StageNotFoundError{Input: input, Available: available}
}
