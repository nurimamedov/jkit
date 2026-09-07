package cmd

import (
	"fmt"
	"os"
	"strconv"
	"strings"

	"github.com/spf13/cobra"

	"github.com/ysmaoui/jkit/internal/jenkins"
	"github.com/ysmaoui/jkit/internal/output"
)

var queueCmd = &cobra.Command{
	Use:   "queue",
	Short: "Show pending builds in the queue",
	Example: `  jkit queue
  jkit queue --job my-app
  jkit queue cancel 12345`,
	Args: cobra.NoArgs,
	RunE: runQueue,
}

var queueCancelCmd = &cobra.Command{
	Use:   "cancel <queue-id>",
	Short: "Cancel a queued build",
	Args:  cobra.ExactArgs(1),
	RunE:  runQueueCancel,
}

func init() {
	queueCmd.Flags().Int("id", 0, "Get a specific queue item (requires --json)")
	queueCmd.Flags().String("job", "", "Filter queue items by job name (substring match)")
	queueCmd.AddCommand(queueCancelCmd)
	rootCmd.AddCommand(queueCmd)
}

func runQueue(cmd *cobra.Command, args []string) error {
	id, _ := cmd.Flags().GetInt("id")
	isJSON, _ := cmd.Flags().GetBool("json")
	if cmd.Flags().Changed("id") && (id <= 0 || !isJSON || cmd.Flags().Changed("job")) {
		return fmt.Errorf("--id requires a positive queue ID and --json, without --job")
	}

	client, _, err := clientFromCmd(cmd)
	if err != nil {
		return err
	}

	if id > 0 {
		item, err := client.GetQueueItem(id)
		if err != nil {
			return err
		}
		return output.NewFormatter(os.Stdout, true, "").Output(item, nil)
	}

	items, err := client.GetQueue()
	if err != nil {
		return err
	}

	// Filter by job name if specified
	jobFilter, _ := cmd.Flags().GetString("job")
	if jobFilter != "" {
		lower := strings.ToLower(jobFilter)
		var filtered []jenkins.QueueItem
		for _, item := range items {
			if strings.Contains(strings.ToLower(item.Task.Name), lower) {
				filtered = append(filtered, item)
			}
		}
		items = filtered
	}

	tmpl, _ := cmd.Flags().GetString("format")
	f := output.NewFormatter(os.Stdout, isJSON, tmpl)

	if isJSON || tmpl != "" {
		return f.Output(items, nil)
	}

	if len(items) == 0 {
		_, _ = fmt.Fprintln(os.Stderr, "Queue is empty")
		return nil
	}

	rows := make([]any, len(items))
	for i := range items {
		rows[i] = items[i]
	}

	columns := []output.Column{
		{Header: "ID", Field: func(v any) string {
			return strconv.Itoa(v.(jenkins.QueueItem).ID)
		}},
		{Header: "JOB", Field: func(v any) string {
			return v.(jenkins.QueueItem).Task.Name
		}},
		{Header: "REASON", Field: func(v any) string {
			return v.(jenkins.QueueItem).Why
		}},
	}

	return f.Output(rows, columns)
}

func runQueueCancel(cmd *cobra.Command, args []string) error {
	id, err := strconv.Atoi(args[0])
	if err != nil {
		return fmt.Errorf("invalid queue ID: %s", args[0])
	}

	client, _, err := clientFromCmd(cmd.Parent())
	if err != nil {
		return err
	}

	if err := client.CancelQueueItem(id); err != nil {
		return err
	}

	_, _ = fmt.Fprintf(os.Stderr, "Cancelled queue item #%d\n", id)
	return nil
}
