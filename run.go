package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tetsuo/colabctl/internal/auth"
	"github.com/tetsuo/colabctl/internal/colab"
	"github.com/tetsuo/colabctl/internal/drive"
	"github.com/tetsuo/colabctl/internal/notebook"
)

var runCmd = &cobra.Command{
	Use:   "run <file-id>",
	Short: "Execute all code cells in a Colab notebook",
	Long: `Starts a new Colab runtime for the given notebook (identified by its
Drive file ID) and runs every code cell in order, printing the output.

Get the file ID from:

  colabctl list`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		requireCredentials()

		fileID := args[0]
		ctx := cmd.Context()

		httpClient, err := auth.Client(ctx, clientID, clientSecret)
		if err != nil {
			return err
		}

		// Download the notebook so we can iterate its cells.
		fmt.Fprintf(os.Stderr, "Fetching notebook %s …\n", fileID)
		driveClient, err := drive.New(ctx, httpClient)
		if err != nil {
			return err
		}

		raw, err := driveClient.GetNotebook(ctx, fileID)
		if err != nil {
			return err
		}

		nb, err := notebook.Parse(raw)
		if err != nil {
			return err
		}

		codeCells := nb.CodeCells()
		if len(codeCells) == 0 {
			fmt.Println("No code cells found in the notebook.")
			return nil
		}

		accelerator, _ := cmd.Flags().GetString("accelerator")
		outputDir, _ := cmd.Flags().GetString("output-dir")
		fmt.Fprintf(os.Stderr, "Starting Colab runtime …\n")
		colabClient := colab.New(httpClient)
		assignment, err := colabClient.Assign(ctx, notebookHash(), "", accelerator)
		if err != nil {
			return fmt.Errorf("start runtime: %w", err)
		}
		fmt.Fprintf(os.Stderr, "Runtime assigned (endpoint %s)\n", assignment.Endpoint)

		kernel, err := colab.CreateAndConnectKernel(ctx, assignment)
		if err != nil {
			return fmt.Errorf("connect to kernel: %w", err)
		}
		defer kernel.Close()

		for i, cell := range codeCells {
			code := cell.Source.String()
			if code == "" {
				continue
			}

			fmt.Printf("\n─── Cell %d ───────────────────────────────────\n", i+1)
			fmt.Println(code)
			fmt.Println("──────────────────────────────────────────────")

			outCh, err := kernel.Execute(ctx, code)
			if err != nil {
				return fmt.Errorf("execute cell %d: %w", i+1, err)
			}

			printOutputs(outCh, outputDir)
		}

		fmt.Fprintln(os.Stderr, "\nAll cells executed.")
		return nil
	},
}

func init() {
	runCmd.Flags().StringP("accelerator", "a", "", "Accelerator for the runtime: T4, A100, TPU, L4, A100-80GB (default: CPU)")
	runCmd.Flags().StringP("output-dir", "o", "", "Directory to save binary outputs (images, audio, video) into (default: current directory)")
	rootCmd.AddCommand(runCmd)
}
