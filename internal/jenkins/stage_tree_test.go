package jenkins

import (
	"fmt"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestNonContainerStages_Flat(t *testing.T) {
	stages := []Stage{
		{ID: "1", Name: "Build"},
		{ID: "2", Name: "Test"},
	}
	got := NonContainerStages(stages)
	assert.Len(t, got, 2)
	assert.Equal(t, "Build", got[0].Name)
	assert.Equal(t, "Test", got[1].Name)
}

func TestNonContainerStages_Parallel(t *testing.T) {
	// Parallel (fan-out with 2 children) is filtered; branches + Build kept
	stages := []Stage{
		{ID: "1", Name: "Build"},
		{ID: "2", Name: "Parallel", FirstParent: "1"},
		{ID: "3", Name: "branch-a", FirstParent: "2"},
		{ID: "4", Name: "branch-b", FirstParent: "2"},
	}
	got := NonContainerStages(stages)
	assert.Len(t, got, 3)
	assert.Equal(t, "Build", got[0].Name)
	assert.Equal(t, "branch-a", got[1].Name)
	assert.Equal(t, "branch-b", got[2].Name)
}

func TestNonContainerStages_SequentialKept(t *testing.T) {
	// Sequential chain: each stage has one child — all kept
	stages := []Stage{
		{ID: "1", Name: "Build"},
		{ID: "2", Name: "Test", FirstParent: "1"},
		{ID: "3", Name: "Deploy", FirstParent: "2"},
	}
	got := NonContainerStages(stages)
	assert.Len(t, got, 3)
}

func TestNonContainerStages_DeeplyNested(t *testing.T) {
	// Two fan-outs (1 and 4) filtered; sequential stages within branches kept
	stages := []Stage{
		{ID: "1", Name: "Parallel"},
		{ID: "2", Name: "branch-a", FirstParent: "1"},
		{ID: "3", Name: "branch-b", FirstParent: "1"},
		{ID: "4", Name: "inner-parallel", FirstParent: "2"},
		{ID: "5", Name: "sub-a", FirstParent: "4"},
		{ID: "6", Name: "sub-b", FirstParent: "4"},
		{ID: "7", Name: "leaf-b", FirstParent: "3"},
	}
	got := NonContainerStages(stages)
	assert.Len(t, got, 5)
	names := make([]string, len(got))
	for i, s := range got {
		names[i] = s.Name
	}
	assert.Equal(t, []string{"branch-a", "branch-b", "sub-a", "sub-b", "leaf-b"}, names)
}

func TestNonContainerStages_Empty(t *testing.T) {
	assert.Nil(t, NonContainerStages(nil))
	assert.Empty(t, NonContainerStages([]Stage{}))
}

func TestQualifiedStagePaths_Flat(t *testing.T) {
	// Sequential chain — bare names, no qualifying prefix.
	stages := []Stage{
		{ID: "1", Name: "Build"},
		{ID: "2", Name: "Test", FirstParent: "1"},
		{ID: "3", Name: "Deploy", FirstParent: "2"},
	}
	paths, err := QualifiedStagePaths(stages)
	require.NoError(t, err)
	assert.Equal(t, "Build", paths["1"])
	assert.Equal(t, "Test", paths["2"])
	assert.Equal(t, "Deploy", paths["3"])
}

func TestQualifiedStagePaths_ParallelDuplicateNames(t *testing.T) {
	// Two parallel branches each containing a stage with the same name.
	stages := []Stage{
		{ID: "1", Name: "Parallel"},
		{ID: "2", Name: "RemoteExec", FirstParent: "1"},
		{ID: "3", Name: "RemoteCache", FirstParent: "1"},
		{ID: "4", Name: "Run Bazel Build", FirstParent: "2"},
		{ID: "5", Name: "Run Bazel Build", FirstParent: "3"},
	}
	paths, err := QualifiedStagePaths(stages)
	require.NoError(t, err)
	// Branch heads get their bare name.
	assert.Equal(t, "RemoteExec", paths["2"])
	assert.Equal(t, "RemoteCache", paths["3"])
	// Inner stages are disambiguated by their enclosing branch.
	assert.Equal(t, "RemoteExec/Run Bazel Build", paths["4"])
	assert.Equal(t, "RemoteCache/Run Bazel Build", paths["5"])
}

func TestQualifiedStagePaths_SequentialWithinBranch(t *testing.T) {
	// A branch with two sequential stages — only the branch head qualifies,
	// the sequential successor inherits the same branch prefix.
	stages := []Stage{
		{ID: "1", Name: "Parallel"},
		{ID: "2", Name: "Linux", FirstParent: "1"},
		{ID: "3", Name: "Windows", FirstParent: "1"},
		{ID: "4", Name: "Compile", FirstParent: "2"},
		{ID: "5", Name: "Package", FirstParent: "4"},
	}
	paths, err := QualifiedStagePaths(stages)
	require.NoError(t, err)
	assert.Equal(t, "Linux", paths["2"])
	assert.Equal(t, "Linux/Compile", paths["4"])
	assert.Equal(t, "Linux/Package", paths["5"])
}

func TestQualifiedStagePaths_NestedParallel(t *testing.T) {
	// Parallel inside a parallel branch yields a multi-segment path.
	stages := []Stage{
		{ID: "1", Name: "Outer"},
		{ID: "2", Name: "X", FirstParent: "1"},
		{ID: "3", Name: "Y", FirstParent: "1"},
		{ID: "4", Name: "Inner", FirstParent: "2"},
		{ID: "5", Name: "A", FirstParent: "4"},
		{ID: "6", Name: "B", FirstParent: "4"},
	}
	paths, err := QualifiedStagePaths(stages)
	require.NoError(t, err)
	assert.Equal(t, "X", paths["2"])
	assert.Equal(t, "X/A", paths["5"])
	assert.Equal(t, "X/B", paths["6"])
}

func TestQualifiedStagePaths_Empty(t *testing.T) {
	for _, stages := range [][]Stage{nil, {}} {
		paths, err := QualifiedStagePaths(stages)
		require.NoError(t, err)
		assert.Empty(t, paths)
	}
}

func TestBuildStageTree_Empty(t *testing.T) {
	for _, stages := range [][]Stage{nil, {}} {
		tree, err := BuildStageTree(stages)
		require.NoError(t, err)
		assert.Nil(t, tree)
	}
}

func TestBuildStageTree_NoParents(t *testing.T) {
	stages := []Stage{
		{ID: "1", Name: "Build"},
		{ID: "2", Name: "Test"},
		{ID: "3", Name: "Deploy"},
	}
	got, err := BuildStageTree(stages)
	require.NoError(t, err)
	assert.Len(t, got, 3)
	for _, s := range got {
		assert.Equal(t, 0, s.Depth)
	}
	assert.Equal(t, "Build", got[0].Name)
	assert.Equal(t, "Test", got[1].Name)
	assert.Equal(t, "Deploy", got[2].Name)
}

func TestBuildStageTree_ParallelWithChildren(t *testing.T) {
	stages := []Stage{
		{ID: "1", Name: "Build"},
		{ID: "2", Name: "Parallel", FirstParent: "1"},
		{ID: "3", Name: "branch-a", FirstParent: "2"},
		{ID: "4", Name: "branch-b", FirstParent: "2"},
		{ID: "5", Name: "branch-c", FirstParent: "2"},
	}
	got, err := BuildStageTree(stages)
	require.NoError(t, err)
	assert.Len(t, got, 5)

	assert.Equal(t, "Build", got[0].Name)
	assert.Equal(t, 0, got[0].Depth)

	// Parallel is single child of Build → same depth
	assert.Equal(t, "Parallel", got[1].Name)
	assert.Equal(t, 0, got[1].Depth)

	// Branches are multiple children of Parallel → depth+1
	assert.Equal(t, "branch-a", got[2].Name)
	assert.Equal(t, 1, got[2].Depth)

	assert.Equal(t, "branch-b", got[3].Name)
	assert.Equal(t, 1, got[3].Depth)

	assert.Equal(t, "branch-c", got[4].Name)
	assert.Equal(t, 1, got[4].Depth)
}

func TestBuildStageTree_NestedParallel(t *testing.T) {
	stages := []Stage{
		{ID: "1", Name: "Outer Parallel"},
		{ID: "2", Name: "Branch-A", FirstParent: "1"},
		{ID: "3", Name: "Branch-B", FirstParent: "1"},
		{ID: "4", Name: "Inner Parallel", FirstParent: "2"},
		{ID: "5", Name: "Sub-A", FirstParent: "4"},
		{ID: "6", Name: "Sub-B", FirstParent: "4"},
	}
	got, err := BuildStageTree(stages)
	require.NoError(t, err)
	assert.Len(t, got, 6)
	assert.Equal(t, 0, got[0].Depth) // Outer Parallel (root)
	assert.Equal(t, 1, got[1].Depth) // Branch-A (child of fan-out)
	assert.Equal(t, 2, got[2].Depth) // Inner Parallel (successor of branch head → depth+1)
	assert.Equal(t, 3, got[3].Depth) // Sub-A (child of inner fan-out)
	assert.Equal(t, 3, got[4].Depth) // Sub-B (child of inner fan-out)
	assert.Equal(t, 1, got[5].Depth) // Branch-B
}

func TestBuildStageTree_SequentialChain(t *testing.T) {
	// Sequential stages chain via firstParent but should all stay at depth 0
	stages := []Stage{
		{ID: "1", Name: "Build"},
		{ID: "2", Name: "Test", FirstParent: "1"},
		{ID: "3", Name: "Deploy", FirstParent: "2"},
	}
	got, err := BuildStageTree(stages)
	require.NoError(t, err)
	assert.Len(t, got, 3)
	for _, s := range got {
		assert.Equal(t, 0, s.Depth, "sequential stage %q should be at depth 0", s.Name)
	}
}

func TestBuildStageTree_OrphanParent(t *testing.T) {
	stages := []Stage{
		{ID: "1", Name: "Build"},
		{ID: "2", Name: "Orphan", FirstParent: "999"},
	}
	got, err := BuildStageTree(stages)
	require.NoError(t, err)
	assert.Len(t, got, 2)
	assert.Equal(t, 0, got[0].Depth)
	assert.Equal(t, 0, got[1].Depth)
}

func TestBuildStageTree_PreservesOrder(t *testing.T) {
	stages := []Stage{
		{ID: "1", Name: "Setup"},
		{ID: "10", Name: "Parallel", FirstParent: "1"},
		{ID: "11", Name: "A", FirstParent: "10"},
		{ID: "12", Name: "B", FirstParent: "10"},
		{ID: "13", Name: "C", FirstParent: "10"},
		{ID: "20", Name: "Teardown"},
	}
	got, err := BuildStageTree(stages)
	require.NoError(t, err)
	names := make([]string, len(got))
	for i, s := range got {
		names[i] = s.Name
	}
	assert.Equal(t, []string{"Setup", "Parallel", "A", "B", "C", "Teardown"}, names)
}

func TestBuildStageTree_BranchSubStagesGrouped(t *testing.T) {
	// Sub-stages within a branch are grouped under the branch header
	stages := []Stage{
		{ID: "1", Name: "Parallel"},
		{ID: "2", Name: "branch-a", FirstParent: "1"},
		{ID: "3", Name: "branch-b", FirstParent: "1"},
		{ID: "4", Name: "sub-1", FirstParent: "2"},
		{ID: "5", Name: "sub-2", FirstParent: "4"},
		{ID: "6", Name: "sub-3", FirstParent: "5"},
	}
	got, err := BuildStageTree(stages)
	require.NoError(t, err)
	assert.Len(t, got, 6)
	assert.Equal(t, 0, got[0].Depth) // Parallel
	assert.Equal(t, 1, got[1].Depth) // branch-a (branch header)
	assert.Equal(t, 2, got[2].Depth) // sub-1 (under branch-a)
	assert.Equal(t, 2, got[3].Depth) // sub-2 (continuation)
	assert.Equal(t, 2, got[4].Depth) // sub-3 (continuation)
	assert.Equal(t, 1, got[5].Depth) // branch-b (branch header)
}

func TestResolveStageIDByNumericID(t *testing.T) {
	stages := []Stage{{ID: "3", Name: "Build"}, {ID: "3366", Name: "Test"}}
	id, err := ResolveStageID(stages, "3366")
	require.NoError(t, err)
	assert.Equal(t, "3366", id)
}

func TestResolveStageIDNameBeatsID(t *testing.T) {
	stages := []Stage{{ID: "3366", Name: "Build"}, {ID: "7", Name: "3366"}}
	id, err := ResolveStageID(stages, "3366")
	require.NoError(t, err)
	assert.Equal(t, "7", id)
}

func TestResolveStageIDNotFoundListsIDs(t *testing.T) {
	stages := []Stage{{ID: "3", Name: "Build"}}
	_, err := ResolveStageID(stages, "99")
	require.Error(t, err)
	assert.Contains(t, err.Error(), "Build (3)")
	var nf *StageNotFoundError
	assert.ErrorAs(t, err, &nf)
	assert.NotContains(t, err.Error(), "--stage-id", "the jenkins layer names no CLI flags")
}

func TestStageTraversalRejectsMalformedGraphs(t *testing.T) {
	cases := map[string][]Stage{
		"self cycle":         {{ID: "1", Name: "Build", FirstParent: "1"}},
		"cross cycle":        {{ID: "1", FirstParent: "2"}, {ID: "2", FirstParent: "1"}},
		"disconnected cycle": {{ID: "root", Name: "Setup"}, {ID: "1", FirstParent: "2"}, {ID: "2", FirstParent: "1"}},
		"duplicate ID":       {{ID: "1"}, {ID: "1"}},
		"missing ID":         {{Name: "Build"}},
		"excessive graph":    make([]Stage, 10001),
	}
	for name, stages := range cases {
		t.Run(name, func(t *testing.T) {
			require.ErrorIs(t, ValidateStageGraph(stages), ErrInvalidStageGraph)
			paths, err := QualifiedStagePaths(stages)
			require.ErrorIs(t, err, ErrInvalidStageGraph)
			require.Nil(t, paths)
			tree, err := BuildStageTree(stages)
			require.ErrorIs(t, err, ErrInvalidStageGraph)
			require.Nil(t, tree)
			diff, err := CompareStages(nil, stages)
			require.ErrorIs(t, err, ErrInvalidStageGraph)
			require.Nil(t, diff)
		})
	}
}

func TestStagePathsUnorderedParallelAndOmittedAncestor(t *testing.T) {
	stages := []Stage{
		{ID: "4", Name: "Test", FirstParent: "2"},
		{ID: "5", Name: "Test", FirstParent: "3"},
		{ID: "3", Name: "Windows", FirstParent: "omitted"},
		{ID: "2", Name: "Linux", FirstParent: "omitted"},
	}
	paths, err := QualifiedStagePaths(stages)
	require.NoError(t, err)
	require.Equal(t, map[string]string{"2": "Linux", "3": "Windows", "4": "Linux/Test", "5": "Windows/Test"}, paths)
}

func TestStageTraversalLongReversedSequentialChain(t *testing.T) {
	stages := make([]Stage, 10000)
	for i := range stages {
		id := len(stages) - i
		stages[i] = Stage{ID: fmt.Sprint(id), Name: "Build"}
		if id > 1 {
			stages[i].FirstParent = fmt.Sprint(id - 1)
		}
	}
	paths, err := QualifiedStagePaths(stages)
	require.NoError(t, err)
	require.Len(t, paths, len(stages))
	require.Equal(t, "Build", paths["10000"])
	require.Equal(t, "Build", paths["1"])
	tree, err := BuildStageTree(stages)
	require.NoError(t, err)
	require.Len(t, tree, len(stages))
	require.Equal(t, "1", tree[0].ID)
	require.Equal(t, "10000", tree[len(tree)-1].ID)
	for _, stage := range tree {
		require.Zero(t, stage.Depth)
	}
}

// Compact remote metadata can multiply into much larger qualified paths.
func TestQualifiedStagePathsRejectsNestedExpansion(t *testing.T) {
	stages := []Stage{{ID: "root", Name: "Root"}}
	parent := "root"
	for i := 0; i < 130; i++ {
		branch := fmt.Sprintf("branch-%d", i)
		stages = append(stages,
			Stage{ID: branch, Name: strings.Repeat("x", 256), FirstParent: parent},
			Stage{ID: fmt.Sprintf("sibling-%d", i), Name: "Y", FirstParent: parent})
		parent = branch
	}
	allowed, err := QualifiedStagePaths(stages[:129])
	require.NoError(t, err)
	require.Len(t, allowed, 129)
	paths, err := QualifiedStagePaths(stages)
	require.ErrorIs(t, err, ErrInvalidStageGraph, "qualified path expansion must be bounded independently of node count")
	require.Nil(t, paths)
}

func TestBuildStageTreeRejectsExcessiveDisplayExpansion(t *testing.T) {
	// An indented name column is padded to the deepest stage on every row.
	stages := []Stage{{ID: "root", Name: "Root"}}
	parent := "root"
	for i := 0; i < 1500; i++ {
		branch := fmt.Sprintf("branch-%d", i)
		stages = append(stages,
			Stage{ID: branch, Name: "X", FirstParent: parent},
			Stage{ID: fmt.Sprintf("sibling-%d", i), Name: "Y", FirstParent: parent})
		parent = branch
	}
	allowed, err := BuildStageTree(stages[:1501])
	require.NoError(t, err)
	require.Len(t, allowed, 1501)
	tree, err := BuildStageTree(stages)
	require.ErrorIs(t, err, ErrInvalidStageGraph, "stage indentation and padding must be bounded independently of node count")
	require.Nil(t, tree)
}

func TestBuildStageTreeRejectsWideColumnExpansion(t *testing.T) {
	for _, field := range []string{"name", "status"} {
		t.Run(field, func(t *testing.T) {
			stages := make([]Stage, 9)
			for i := range stages {
				stages[i] = Stage{ID: fmt.Sprint(i), Name: "Build", Status: "SUCCESS"}
			}
			if field == "name" {
				stages[0].Name = strings.Repeat("x", 1<<20)
			} else {
				stages[0].Status = strings.Repeat("x", 1<<20)
			}
			tree, err := BuildStageTree(stages)
			require.ErrorIs(t, err, ErrInvalidStageGraph)
			require.Nil(t, tree)
		})
	}
}

func TestQualifiedStagePathsRejectsWideColumnExpansion(t *testing.T) {
	stages := make([]Stage, 9)
	for i := range stages {
		stages[i] = Stage{ID: fmt.Sprint(i), Name: "Build"}
	}
	stages[0].Name = strings.Repeat("x", 1<<20)
	paths, err := QualifiedStagePaths(stages)
	require.ErrorIs(t, err, ErrInvalidStageGraph, "one long path must not expand into an excessive padded table column")
	require.Nil(t, paths)
}

func TestQualifiedStagePathsRejectsCumulativeExpansion(t *testing.T) {
	// Every path fits the padded-column budget, but all paths and newly
	// allocated branch prefixes together exceed the cumulative budget.
	stages := []Stage{
		{ID: "root", Name: "Root"},
		{ID: "branch", Name: strings.Repeat("x", 4096), FirstParent: "root"},
		{ID: "sibling", Name: "Y", FirstParent: "root"},
	}
	for i := 0; i < 1100; i++ {
		stages = append(stages, Stage{ID: fmt.Sprint(i), Name: "Leaf", FirstParent: "branch"})
	}
	paths, err := QualifiedStagePaths(stages)
	require.ErrorIs(t, err, ErrInvalidStageGraph)
	require.Nil(t, paths)
}
