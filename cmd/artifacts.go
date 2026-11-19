package cmd

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"

	"github.com/spf13/cobra"

	"github.com/ysmaoui/jkit/internal/api"
	"github.com/ysmaoui/jkit/internal/jenkins"
	"github.com/ysmaoui/jkit/internal/output"
)

var artifactsCmd = &cobra.Command{
	Use:   "artifacts [job] [build#]",
	Short: "List or download build artifacts",
	Example: `  jkit artifacts my-app 42
  jkit artifacts my-app 42 -d report.xml
  jkit artifacts my-app 42 -d report.xml -o /tmp/report.xml`,
	Args: cobra.MaximumNArgs(2),
	RunE: runArtifacts,
}

func init() {
	artifactsCmd.Flags().Bool("stdout", false, "Write one exact artifact to stdout (64 KiB maximum; requires explicit build)")
	artifactsCmd.Flags().StringP("download", "d", "", "Download artifact by filename")
	artifactsCmd.Flags().StringP("output", "o", "", "Output file path (default: current dir with original filename)")
	artifactsCmd.Flags().String("output-dir", "", "Download exact relative path into this directory (512 MiB maximum; requires explicit build)")
	rootCmd.AddCommand(artifactsCmd)
}

func runArtifacts(cmd *cobra.Command, args []string) error {
	stdout, _ := cmd.Flags().GetBool("stdout")
	downloadName, _ := cmd.Flags().GetString("download")
	outputPath, _ := cmd.Flags().GetString("output")
	if stdout {
		if downloadName == "" || cmd.Flags().Changed("output") {
			return fmt.Errorf("--stdout requires --download and cannot be combined with --output")
		}
		if err := api.ValidateArtifactPath(downloadName); err != nil {
			return err
		}
	}

	client, jobPath, buildNum, err := resolveJobArgs(cmd, args, false)
	if err != nil {
		return err
	}

	if cmd.Flags().Changed("output-dir") {
		dir, _ := cmd.Flags().GetString("output-dir")
		if dir == "" || stdout || cmd.Flags().Changed("output") || downloadName == "" {
			return fmt.Errorf("--output-dir requires --download and cannot be combined with --stdout or --output")
		}
		body, err := client.OpenArtifactFile(cmd.Context(), jobPath, buildNum, downloadName)
		if err != nil {
			return err
		}
		defer body.Close()
		if err := os.MkdirAll(dir, 0700); err != nil {
			return err
		}
		n, err := output.SaveArtifact(filepath.Join(dir, filepath.Base(downloadName)), body)
		if err != nil {
			return err
		}
		return json.NewEncoder(cmd.OutOrStdout()).Encode(map[string]int64{"downloadedBytes": n})
	}
	if stdout {
		data, err := client.ReadArtifactBounded(jobPath, buildNum, downloadName)
		if err != nil {
			return err
		}
		_, err = os.Stdout.Write(data)
		return err
	}

	// Default to latest build
	if buildNum == 0 {
		builds, err := client.GetBuilds(jobPath, 1)
		if err != nil {
			return err
		}
		if len(builds) == 0 {
			return fmt.Errorf("no builds found for %s", jobPath)
		}
		buildNum = builds[0].Number
	}

	if downloadName != "" {
		return downloadArtifact(client, jobPath, buildNum, downloadName, outputPath)
	}

	// List artifacts
	artifacts, err := client.GetArtifacts(jobPath, buildNum)
	if err != nil {
		return err
	}

	isJSON, _ := cmd.Flags().GetBool("json")
	tmpl, _ := cmd.Flags().GetString("format")
	f := output.NewFormatter(os.Stdout, isJSON, tmpl)

	if isJSON || tmpl != "" {
		return f.Output(artifacts, nil)
	}

	if len(artifacts) == 0 {
		_, _ = fmt.Fprintf(os.Stderr, "No artifacts for build #%d\n", buildNum)
		return nil
	}

	rows := make([]any, len(artifacts))
	for i := range artifacts {
		rows[i] = artifacts[i]
	}

	columns := []output.Column{
		{Header: "FILE", Field: func(v any) string {
			return v.(jenkins.Artifact).FileName
		}},
		{Header: "PATH", Field: func(v any) string {
			return v.(jenkins.Artifact).RelativePath
		}},
	}

	return f.Output(rows, columns)
}

func downloadArtifact(client *api.Client, jobPath string, buildNum int, name, outputPath string) error {
	// Find the artifact
	artifacts, err := client.GetArtifacts(jobPath, buildNum)
	if err != nil {
		return err
	}

	var match *jenkins.Artifact
	for i := range artifacts {
		if artifacts[i].FileName == name || artifacts[i].RelativePath == name {
			match = &artifacts[i]
			break
		}
	}
	if match == nil {
		return fmt.Errorf("artifact %q not found in build #%d", name, buildNum)
	}

	body, err := client.DownloadArtifact(jobPath, buildNum, match.RelativePath)
	if err != nil {
		return err
	}
	defer func() { _ = body.Close() }()

	if outputPath == "" {
		outputPath = filepath.Base(match.FileName)
	}

	n, err := output.SaveArtifact(outputPath, body)
	if err != nil {
		return err
	}

	_, _ = fmt.Fprintf(os.Stderr, "Downloaded %s (%d bytes)\n", outputPath, n)
	return nil
}
