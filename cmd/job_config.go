package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"time"

	"github.com/spf13/cobra"
	"github.com/ysmaoui/jkit/internal/api"
)

func init() {
	command := &cobra.Command{Use: "job-config", Short: "Read or replace exact job configuration bytes"}
	get := &cobra.Command{Use: "get JOB --stdout", Short: "Read bounded job XML to stdout", Args: cobra.ExactArgs(1), RunE: runJobConfig}
	update := &cobra.Command{Use: "update JOB --stdin --json", Short: "Submit bounded job XML from stdin once", Args: cobra.ExactArgs(1), RunE: runJobConfig}
	get.Flags().Bool("stdout", false, "Write exact XML bytes to stdout (1 MiB maximum)")
	update.Flags().Bool("stdin", false, "Read exact XML bytes from stdin (1 MiB maximum)")
	copy := &cobra.Command{Use: "copy SOURCE DEST --json --expected-sha256 HASH", Short: "Create an absent job from approved exact XML once", Args: cobra.ExactArgs(2), RunE: runCopyJobConfig}
	copy.Flags().String("expected-sha256", "", "Required SHA-256 of the approved exact source XML")
	digest := &cobra.Command{Use: "digest JOB --json", Short: "Read exact and normalized configuration hashes", Args: cobra.ExactArgs(1), RunE: runDigestJobConfig}
	create := &cobra.Command{Use: "create-scm JOB --scm-branch BRANCH --script-path FILE --json", Short: "Create one standalone Pipeline from SCM", Args: cobra.ExactArgs(1), RunE: runCreateSCMJob}
	create.Flags().String("scm-branch", "", "Literal CI repository branch")
	create.Flags().String("script-path", "", "Pipeline script relative to repository root")
	command.AddCommand(get, update, copy, digest, create)
	rootCmd.AddCommand(command)
}

func runJobConfig(cmd *cobra.Command, args []string) error {
	job := args[0]
	if err := api.ValidateConfigJob(job); err != nil {
		return err
	}
	isJSON, _ := cmd.Flags().GetBool("json")
	toStdout, _ := cmd.Flags().GetBool("stdout")
	fromStdin, _ := cmd.Flags().GetBool("stdin")
	verbose, _ := cmd.Flags().GetBool("verbose")
	format, _ := cmd.Flags().GetString("format")
	branch, _ := cmd.Flags().GetString("branch")
	update := cmd.Name() == "update"
	if verbose || format != "" || branch != "" || update && (!fromStdin || !isJSON) || !update && (!toStdout || isJSON) {
		return fmt.Errorf("job-config requires get --stdout or update --stdin --json, without --verbose, --format or --branch")
	}
	rawTimeout, _ := cmd.Flags().GetString("timeout")
	timeout, err := time.ParseDuration(rawTimeout)
	if err != nil || timeout <= 0 || timeout > 120*time.Second {
		return fmt.Errorf("job-config timeout must be positive and at most 120s")
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()
	var data []byte
	if update {
		data, err = readJobConfigStdin(ctx)
		if err != nil {
			return err
		}
		if err := api.ValidateConfigXML(data); err != nil {
			return err
		}
	}
	client, _, err := clientFromCmd(cmd)
	if err != nil {
		return fmt.Errorf("cannot load Jenkins credentials for job configuration")
	}
	if update {
		if err := client.UpdateJobConfig(ctx, job, data); err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(struct {
			Job             string `json:"job"`
			SubmittedSHA256 string `json:"submittedSha256"`
			Accepted        bool   `json:"accepted"`
		}{job, fmt.Sprintf("%x", sha256.Sum256(data)), true})
	}
	data, err = client.GetJobConfig(ctx, job)
	if err != nil {
		return err
	}
	_, err = os.Stdout.Write(data)
	return err
}

func readJobConfigStdin(ctx context.Context) ([]byte, error) {
	input := os.Stdin
	type result struct {
		data []byte
		err  error
	}
	done := make(chan result, 1)
	go func() {
		data, err := io.ReadAll(io.LimitReader(input, api.MaxJobConfigBytes+1))
		done <- result{data, err}
	}()
	select {
	case <-ctx.Done():
		_ = input.Close()
		return nil, fmt.Errorf("job configuration stdin timed out")
	case value := <-done:
		if value.err != nil {
			return nil, fmt.Errorf("cannot read job configuration stdin")
		}
		return value.data, nil
	}
}

func runCopyJobConfig(cmd *cobra.Command, args []string) error {
	isJSON, _ := cmd.Flags().GetBool("json")
	verbose, _ := cmd.Flags().GetBool("verbose")
	format, _ := cmd.Flags().GetString("format")
	branch, _ := cmd.Flags().GetString("branch")
	if !isJSON || verbose || format != "" || branch != "" {
		return fmt.Errorf("job-config copy requires --json without --verbose, --format or --branch")
	}
	for _, job := range args {
		if err := api.ValidateConfigJob(job); err != nil {
			return err
		}
	}
	expected, _ := cmd.Flags().GetString("expected-sha256")
	rawTimeout, _ := cmd.Flags().GetString("timeout")
	timeout, err := time.ParseDuration(rawTimeout)
	if err != nil || timeout <= 0 || timeout > 120*time.Second {
		return fmt.Errorf("job-config timeout must be positive and at most 120s")
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()
	client, _, err := clientFromCmd(cmd)
	if err != nil {
		return fmt.Errorf("cannot load Jenkins credentials for job configuration")
	}
	receipt, err := client.CopyJobConfig(ctx, args[0], args[1], expected)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(receipt)
}

func runCreateSCMJob(cmd *cobra.Command, args []string) error {
	isJSON, _ := cmd.Flags().GetBool("json")
	verbose, _ := cmd.Flags().GetBool("verbose")
	format, _ := cmd.Flags().GetString("format")
	branchFlag, _ := cmd.Flags().GetString("branch")
	if !isJSON || verbose || format != "" || branchFlag != "" {
		return fmt.Errorf("job-config create-scm requires --json without --verbose, --format or --branch")
	}
	rawTimeout, _ := cmd.Flags().GetString("timeout")
	timeout, err := time.ParseDuration(rawTimeout)
	if err != nil || timeout <= 0 || timeout > 120*time.Second {
		return fmt.Errorf("job-config timeout must be positive and at most 120s")
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()
	client, _, err := clientFromCmd(cmd)
	if err != nil {
		return fmt.Errorf("cannot load Jenkins credentials for job configuration")
	}
	branch, _ := cmd.Flags().GetString("scm-branch")
	script, _ := cmd.Flags().GetString("script-path")
	receipt, err := client.CreateSCMJob(ctx, args[0], branch, script)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(receipt)
}

func runDigestJobConfig(cmd *cobra.Command, args []string) error {
	isJSON, _ := cmd.Flags().GetBool("json")
	verbose, _ := cmd.Flags().GetBool("verbose")
	format, _ := cmd.Flags().GetString("format")
	branch, _ := cmd.Flags().GetString("branch")
	if !isJSON || verbose || format != "" || branch != "" {
		return fmt.Errorf("job-config digest requires --json without --verbose, --format or --branch")
	}
	if err := api.ValidateConfigJob(args[0]); err != nil {
		return err
	}
	rawTimeout, _ := cmd.Flags().GetString("timeout")
	timeout, err := time.ParseDuration(rawTimeout)
	if err != nil || timeout <= 0 || timeout > 120*time.Second {
		return fmt.Errorf("job-config timeout must be positive and at most 120s")
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()
	client, _, err := clientFromCmd(cmd)
	if err != nil {
		return fmt.Errorf("cannot load Jenkins credentials for job configuration")
	}
	digest, err := client.DigestJobConfig(ctx, args[0])
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(digest)
}
