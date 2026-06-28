package main

import (
	"fmt"
	"os"
	"text/tabwriter"

	"github.com/spf13/cobra"

	"github.com/tetsuo/colabctl/internal/auth"
	"github.com/tetsuo/colabctl/internal/colab"
	"github.com/tetsuo/colabctl/internal/config"
)

var sessionsCmd = &cobra.Command{
	Use:     "sessions",
	Aliases: []string{"ps"},
	Short:   "List active Colab runtimes on your account",
	RunE: func(cmd *cobra.Command, _ []string) error {
		requireCredentials()

		httpClient, err := auth.Client(cmd.Context(), clientID, clientSecret)
		if err != nil {
			return err
		}
		client := colab.New(httpClient)

		runtimes, err := client.ListAssignments(cmd.Context())
		if err != nil {
			return err
		}

		if len(runtimes) == 0 {
			fmt.Println("No active runtimes.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ENDPOINT\tACCELERATOR\tURL")
		fmt.Fprintln(w, "--------\t-----------\t---")
		for _, r := range runtimes {
			accel := r.Accelerator
			if accel == "" {
				accel = "CPU"
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", r.Endpoint, accel, r.URL)
		}
		return w.Flush()
	},
}

var stopCmd = &cobra.Command{
	Use:   "stop <endpoint>",
	Short: "Stop and release an active Colab runtime",
	Long: `Terminates the runtime identified by its endpoint ID.
Get the endpoint from:

  colabctl sessions`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		requireCredentials()

		endpoint := args[0]
		ctx := cmd.Context()

		httpClient, err := auth.Client(ctx, clientID, clientSecret)
		if err != nil {
			return err
		}
		client := colab.New(httpClient)

		fmt.Fprintf(os.Stderr, "Stopping runtime %s …\n", endpoint)
		if err := client.Unassign(ctx, endpoint); err != nil {
			return fmt.Errorf("stop runtime: %w", err)
		}
		_ = config.DeleteSession(endpoint)
		fmt.Println("Runtime stopped.")
		return nil
	},
}

func init() {
	rootCmd.AddCommand(sessionsCmd)
	rootCmd.AddCommand(stopCmd)
}
