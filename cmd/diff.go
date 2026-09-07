package cmd

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ysmaoui/jkit/internal/api"
	"github.com/ysmaoui/jkit/internal/jenkins"
	"github.com/ysmaoui/jkit/internal/output"
)

var diffCmd = &cobra.Command{
	Use:   "diff [job] [build1] [build2]",
	Short: "Compare two builds of the same job",
	Long:  "Shows differences in parameters, stage outcomes, test results, and commits between two builds. If only one build is given, compares with the previous build. With no build numbers, compares the latest two.",
	Example: `  jkit diff my-app 41 42
  jkit diff my-app 42
  jkit diff my-app`,
	Args: cobra.MaximumNArgs(3),
	RunE: runDiff,
}

func init() {
	rootCmd.AddCommand(diffCmd)
}

// DiffResult is the structured diff between two builds.
type DiffResult struct {
	Job     string              `json:"job"`
	Build1  int                 `json:"build1"`
	Build2  int                 `json:"build2"`
	Result1 string              `json:"result1"`
	Result2 string              `json:"result2"`
	Params  []ParamDiff         `json:"paramDiffs,omitempty"`
	Stages  []jenkins.StageDiff `json:"stageDiffs,omitempty"`
	Tests   []jenkins.TestDiff  `json:"testDiffs,omitempty"`
	Commits []api.CommitSummary `json:"newCommits,omitempty"`
}

// ParamDiff records a changed parameter.
type ParamDiff struct {
	Name string `json:"name"`
	Old  string `json:"old"`
	New  string `json:"new"`
}

func runDiff(cmd *cobra.Command, args []string) error {
	client, jobPath, buildNum, err := resolveJobArgs(cmd, args, false)
	if err != nil {
		return err
	}

	// Determine build numbers
	var b1, b2 int
	switch {
	case buildNum > 0 && len(args) >= 3:
		// jkit diff job 41 42 — buildNum is from args[1], args[2] is the second
		b1 = buildNum
		n, err := parseIntArg(args[2])
		if err != nil {
			return fmt.Errorf("invalid second build number: %s", args[2])
		}
		b2 = n
	case buildNum > 0:
		// jkit diff job 42 — compare with previous
		b2 = buildNum
		b1 = buildNum - 1
	default:
		// jkit diff job — latest two
		builds, err := client.GetBuilds(jobPath, 2)
		if err != nil {
			return err
		}
		if len(builds) < 2 {
			return fmt.Errorf("need at least 2 builds to diff, found %d", len(builds))
		}
		b1 = builds[1].Number
		b2 = builds[0].Number
	}

	if b1 > b2 {
		b1, b2 = b2, b1
	}

	// Fetch both builds
	build1, err := client.GetBuild(jobPath, b1)
	if err != nil {
		return fmt.Errorf("getting build #%d: %w", b1, err)
	}
	build2, err := client.GetBuild(jobPath, b2)
	if err != nil {
		return fmt.Errorf("getting build #%d: %w", b2, err)
	}

	result := &DiffResult{
		Job:     jobPath,
		Build1:  b1,
		Build2:  b2,
		Result1: buildResult(build1),
		Result2: buildResult(build2),
	}

	// Parameter diffs
	result.Params = diffParams(build1, build2)

	// A comparison must not turn unavailable data into an empty difference.
	stages1, err := client.GetPipelineStages(jobPath, b1)
	if err != nil {
		return fmt.Errorf("getting stages for build #%d: %w", b1, err)
	}
	stages2, err := client.GetPipelineStages(jobPath, b2)
	if err != nil {
		return fmt.Errorf("getting stages for build #%d: %w", b2, err)
	}
	for i, stages := range [][]jenkins.Stage{stages1, stages2} {
		if stages == nil {
			return &jenkins.NotFoundError{Resource: "pipeline stages", Name: fmt.Sprintf("build #%d", []int{b1, b2}[i]), Host: client.Host()}
		}
	}
	result.Stages, err = jenkins.CompareStages(stages1, stages2)
	if err != nil {
		return fmt.Errorf("comparing stages: %w", err)
	}
	report1, err := client.GetTestReport(jobPath, b1)
	if err != nil {
		return fmt.Errorf("getting test report for build #%d: %w", b1, err)
	}
	report2, err := client.GetTestReport(jobPath, b2)
	if err != nil {
		return fmt.Errorf("getting test report for build #%d: %w", b2, err)
	}
	for i, report := range []*jenkins.TestReport{report1, report2} {
		if report == nil {
			return &jenkins.NotFoundError{Resource: "test report", Name: fmt.Sprintf("build #%d", []int{b1, b2}[i]), Host: client.Host()}
		}
	}
	result.Tests, err = jenkins.CompareTests(report1, report2)
	if err != nil {
		return fmt.Errorf("comparing tests: %w", err)
	}

	// New commits in build2
	for _, cs := range build2.ChangeSets {
		for _, ch := range cs.Items {
			hash := ch.CommitID
			if len(hash) > 7 {
				hash = hash[:7]
			}
			msg := ch.Message
			if idx := strings.IndexAny(msg, "\n\r"); idx >= 0 {
				msg = msg[:idx]
			}
			result.Commits = append(result.Commits, api.CommitSummary{
				Hash: hash, Author: ch.Author.FullName, Message: msg,
			})
		}
	}

	isJSON, _ := cmd.Flags().GetBool("json")
	tmpl, _ := cmd.Flags().GetString("format")
	f := output.NewFormatter(os.Stdout, isJSON, tmpl)

	if isJSON || tmpl != "" {
		return f.Output(result, nil)
	}

	printDiff(result)
	return nil
}

func buildResult(b *jenkins.Build) string {
	if b.Building {
		return "BUILDING"
	}
	if b.Result == "" {
		return "UNKNOWN"
	}
	return b.Result
}

func diffParams(b1, b2 *jenkins.Build) []ParamDiff {
	p1 := make(map[string]string)
	for _, p := range b1.Parameters() {
		p1[p.Name] = p.Value()
	}
	p2 := make(map[string]string)
	for _, p := range b2.Parameters() {
		p2[p.Name] = p.Value()
	}

	var diffs []ParamDiff
	for k, v2 := range p2 {
		v1, ok := p1[k]
		if !ok || v1 != v2 {
			diffs = append(diffs, ParamDiff{Name: k, Old: v1, New: v2})
		}
	}
	for k, v1 := range p1 {
		if _, ok := p2[k]; !ok {
			diffs = append(diffs, ParamDiff{Name: k, Old: v1, New: "(removed)"})
		}
	}
	return diffs
}

func parseIntArg(s string) (int, error) {
	var n int
	_, err := fmt.Sscanf(s, "%d", &n)
	return n, err
}

func printDiff(r *DiffResult) {
	_, _ = fmt.Fprintf(os.Stdout, "Comparing #%d (%s) → #%d (%s)\n",
		r.Build1, output.ColorStatus(r.Result1),
		r.Build2, output.ColorStatus(r.Result2))

	if len(r.Params) > 0 {
		_, _ = fmt.Fprintln(os.Stdout, "\nParameter Changes:")
		for _, p := range r.Params {
			_, _ = fmt.Fprintf(os.Stdout, "  %s: %s → %s\n", p.Name, p.Old, p.New)
		}
	}

	if len(r.Stages) > 0 {
		_, _ = fmt.Fprintln(os.Stdout, "\nStage Changes:")
		for _, s := range r.Stages {
			old := s.Old
			if old == "" {
				old = "(new)"
			}
			_, _ = fmt.Fprintf(os.Stdout, "  %s: %s → %s\n", s.Path, old, output.ColorStatus(s.New))
		}
	}

	if len(r.Tests) > 0 {
		_, _ = fmt.Fprintln(os.Stdout, "\nTest Changes:")
		for _, t := range r.Tests {
			old := t.Old
			if old == "" {
				old = "(new)"
			}
			_, _ = fmt.Fprintf(os.Stdout, "  %s > %s.%s: %s → %s\n", t.Suite, t.ClassName, t.Test, old, t.New)
		}
	}

	if len(r.Commits) > 0 {
		_, _ = fmt.Fprintf(os.Stdout, "\nCommits in #%d:\n", r.Build2)
		for _, c := range r.Commits {
			msg := c.Message
			if len(msg) > 60 {
				msg = msg[:60] + "..."
			}
			_, _ = fmt.Fprintf(os.Stdout, "  %s %s — %s\n", c.Hash, c.Author, msg)
		}
	}

	if len(r.Params) == 0 && len(r.Stages) == 0 && len(r.Tests) == 0 && len(r.Commits) == 0 {
		_, _ = fmt.Fprintln(os.Stdout, "\nNo differences found")
	}
}
