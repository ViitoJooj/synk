package cli

import "github.com/spf13/cobra"

var (
	logsFollow   bool
	logsLines    int
	logsInjector bool
)

var logsCmd = &cobra.Command{
	Use:   "logs",
	Short: "Show what the agent has been saying",
	Long: `Show the agent's recent output: connections, disconnections, and why.

On Linux this is the journal of the agent's unit (--injector for the injector's).
On Windows it is the log file the scheduled task writes.`,
	Args: cobra.NoArgs,
	RunE: runLogs,
}

func init() {
	logsCmd.Flags().BoolVarP(&logsFollow, "follow", "f", false, "keep printing new lines as they arrive")
	logsCmd.Flags().IntVarP(&logsLines, "lines", "n", 50, "how many recent lines to show")
	rootCmd.AddCommand(logsCmd)
}
