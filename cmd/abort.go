package cmd

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strconv"
	"time"

	"github.com/spf13/cobra"

	"github.com/ysmaoui/jkit/internal/api"
	"github.com/ysmaoui/jkit/internal/output"
)

var abortCmd = &cobra.Command{
	Use:   "abort [job] [build#]",
	Short: "Abort a running build",
	Example: `  jkit abort my-app
  jkit abort my-app 42
  jkit abort my-app 42 --wait`,
	Args: cobra.MaximumNArgs(2),
	RunE: runAbort,
}

func init() {
	abortCmd.Flags().BoolP("wait", "w", false, "Wait until the build actually stops")
	rootCmd.AddCommand(abortCmd)
}

func runAbort(cmd *cobra.Command, args []string) error {
	isJSON, _ := cmd.Flags().GetBool("json")
	if isJSON {
		return runAbortJSON(cmd, args)
	}

	client, jobPath, buildNum, err := resolveJobArgs(cmd, args, false)
	if err != nil {
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

	// Check if build is running
	build, err := client.GetBuild(jobPath, buildNum)
	if err != nil {
		return err
	}
	if !build.Building {
		_, _ = fmt.Fprintf(os.Stderr, "Build #%d is not running (result: %s)\n", buildNum, build.Result)
		return nil
	}

	if err := client.StopBuild(jobPath, buildNum); err != nil {
		return err
	}

	wait, _ := cmd.Flags().GetBool("wait")
	if !wait {
		_, _ = fmt.Fprintf(os.Stderr, "Build #%d abort signal sent\n", buildNum)
		return nil
	}

	_, _ = fmt.Fprintf(os.Stderr, "Waiting for build #%d to stop...\n", buildNum)
	start := time.Now()
	timeout := 2 * time.Minute
	for {
		time.Sleep(2 * time.Second)
		b, err := client.GetBuild(jobPath, buildNum)
		if err != nil {
			return fmt.Errorf("polling build status: %w", err)
		}
		if !b.Building {
			elapsed := time.Since(start).Truncate(time.Second)
			_, _ = fmt.Fprintf(os.Stderr, "Build #%d stopped — %s (%s)\n", buildNum, output.ColorStatus(b.Result), elapsed)
			return nil
		}
		if time.Since(start) > timeout {
			_, _ = fmt.Fprintf(os.Stderr, "warning: build #%d still running after %s — it may have cleanup steps or finally blocks\n", buildNum, timeout)
			return nil
		}
	}
}

func runAbortJSON(cmd *cobra.Command, args []string) error {
	wait, _ := cmd.Flags().GetBool("wait")
	verbose, _ := cmd.Flags().GetBool("verbose")
	format, _ := cmd.Flags().GetString("format")
	branch, _ := cmd.Flags().GetString("branch")
	if len(args) != 2 || wait || verbose || format != "" || branch != "" {
		return fmt.Errorf("abort --json requires JOB BUILD without --wait, --verbose, --format or --branch")
	}
	if err := api.ValidateConfigJob(args[0]); err != nil {
		return err
	}
	number, err := strconv.Atoi(args[1])
	if err != nil || number <= 0 || strconv.Itoa(number) != args[1] {
		return fmt.Errorf("abort --json requires a positive explicit build number")
	}
	rawTimeout, _ := cmd.Flags().GetString("timeout")
	timeout, err := time.ParseDuration(rawTimeout)
	if err != nil || timeout <= 0 || timeout > 120*time.Second {
		return fmt.Errorf("abort timeout must be positive and at most 120s")
	}
	ctx, cancel := context.WithTimeout(cmd.Context(), timeout)
	defer cancel()
	client, _, err := clientFromCmd(cmd)
	if err != nil {
		return fmt.Errorf("cannot load Jenkins credentials for abort")
	}
	receipt, err := client.AbortBuildOnce(ctx, args[0], number)
	if err != nil {
		return err
	}
	return json.NewEncoder(os.Stdout).Encode(receipt)
}
