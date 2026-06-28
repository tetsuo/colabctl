package main

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/spf13/cobra"

	"github.com/tetsuo/colabctl/internal/auth"
	"github.com/tetsuo/colabctl/internal/colab"
	"github.com/tetsuo/colabctl/internal/config"
)

var execCmd = &cobra.Command{
	Use:   "exec <file-id>",
	Short: "Open an interactive REPL connected to a Colab runtime",
	Long: `Connects to a Colab runtime and opens an interactive REPL.

By default a new runtime is allocated. If you already have a runtime running
(see 'colabctl sessions'), pass its endpoint with --session to reuse it instead
of allocating a new one. If only one runtime is active it is reused
automatically.

Get the file ID from:

  colabctl list`,
	Args: cobra.ExactArgs(1),
	RunE: func(cmd *cobra.Command, args []string) error {
		requireCredentials()

		fileID := args[0]
		ctx := cmd.Context()
		sessionEndpoint, _ := cmd.Flags().GetString("session")
		accelerator, _ := cmd.Flags().GetString("accelerator")
		outputDir, _ := cmd.Flags().GetString("output-dir")

		httpClient, err := auth.Client(ctx, clientID, clientSecret)
		if err != nil {
			return err
		}

		colabClient := colab.New(httpClient)

		var assignment *colab.AssignmentInfo

		// resolveFromCache tries the local session cache for the given endpoint.
		// Returns nil (no error) when the cached entry is missing or expired.
		resolveFromCache := func(endpoint string) *colab.AssignmentInfo {
			s, err := config.LoadSession(endpoint)
			if err != nil || s == nil || !s.Valid() {
				return nil
			}
			fmt.Fprintf(os.Stderr, "Using cached session (endpoint %s, token valid until %s)\n\n",
				s.Endpoint, s.TokenExpiresAt.Format("15:04:05"))
			return &colab.AssignmentInfo{
				Endpoint: s.Endpoint,
				RuntimeProxyInfo: colab.RuntimeProxyInfo{
					Token: s.Token,
					URL:   s.URL,
				},
			}
		}

		if sessionEndpoint != "" {
			// User explicitly named an endpoint; check cache first.
			if a := resolveFromCache(sessionEndpoint); a != nil {
				assignment = a
			} else {
				assignment, err = colabClient.AssignmentFromEndpoint(ctx, sessionEndpoint)
				if err != nil {
					return fmt.Errorf("connect to session %s: %w", sessionEndpoint, err)
				}
				fmt.Fprintf(os.Stderr, "Connected to existing runtime (endpoint %s)\n\n", assignment.Endpoint)
			}
		} else {
			// Check whether the user already has active runtimes.
			runtimes, listErr := colabClient.ListAssignments(ctx)
			if listErr == nil && len(runtimes) == 1 {
				fmt.Fprintf(os.Stderr, "Reusing existing runtime (endpoint %s)\n", runtimes[0].Endpoint)
				fmt.Fprintf(os.Stderr, "  Tip: use --session <endpoint> to choose a specific one.\n\n")
				if a := resolveFromCache(runtimes[0].Endpoint); a != nil {
					assignment = a
				} else {
					assignment, err = colabClient.AssignmentFromEndpoint(ctx, runtimes[0].Endpoint)
					if err != nil {
						return fmt.Errorf("connect to existing runtime: %w", err)
					}
				}
			} else if listErr == nil && len(runtimes) > 1 {
				fmt.Fprintf(os.Stderr, "You have %d active runtimes. Use --session <endpoint> to pick one:\n\n", len(runtimes))
				for _, r := range runtimes {
					accel := r.Accelerator
					if accel == "" {
						accel = "CPU"
					}
					fmt.Fprintf(os.Stderr, "  %s  (%s)\n", r.Endpoint, accel)
				}
				fmt.Fprintln(os.Stderr)
				return fmt.Errorf("specify a runtime with --session <endpoint>")
			} else {
				// No existing runtimes (or list failed); allocate a new one.
				fmt.Fprintf(os.Stderr, "Starting Colab runtime for %s …\n", fileID)
				assignment, err = colabClient.Assign(ctx, notebookHash(), "", accelerator)
				if err != nil {
					return fmt.Errorf("start runtime: %w", err)
				}
				fmt.Fprintf(os.Stderr, "Runtime ready (endpoint %s)\n\n", assignment.Endpoint)
			}
		}

		// Persist the assignment so the next invocation skips the assign round-trip.
		_ = config.SaveSession(&config.SessionState{
			Endpoint:       assignment.Endpoint,
			URL:            assignment.RuntimeProxyInfo.URL,
			Token:          assignment.RuntimeProxyInfo.Token,
			TokenExpiresAt: time.Now().Add(time.Duration(assignment.RuntimeProxyInfo.TokenExpiresInSecs) * time.Second),
		})

		kernel, err := colab.CreateAndConnectKernel(ctx, assignment)
		if err != nil {
			return fmt.Errorf("connect to kernel: %w", err)
		}
		defer kernel.Close()

		// Non-interactive mode: stdin is a pipe; read all input and execute once.
		if fi, err := os.Stdin.Stat(); err == nil && fi.Mode()&os.ModeCharDevice == 0 {
			input, err := io.ReadAll(os.Stdin)
			if err != nil {
				return fmt.Errorf("read stdin: %w", err)
			}
			code := strings.TrimRight(string(input), "\n")
			if strings.TrimSpace(code) == "" {
				return nil
			}
			outCh, err := kernel.Execute(ctx, code)
			if err != nil {
				return fmt.Errorf("execute: %w", err)
			}
			printOutputs(outCh, outputDir)
			return nil
		}

		// Interactive REPL.
		scanner := bufio.NewScanner(os.Stdin)
		// Allow large pastes.
		scanner.Buffer(make([]byte, 1024*1024), 1024*1024)

		fmt.Println(`Colab REPL; connected to a live Colab kernel.
Type Python code and press Enter to run a single line.
To run a block: paste or type multiple lines, then press Enter twice on a blank line.
Commands: :quit  :exit  :help`)

		for {
			fmt.Print(">>> ")
			var lines []string
			prevBlank := false
			for {
				if !scanner.Scan() {
					fmt.Println()
					return scanner.Err()
				}
				line := scanner.Text()

				if len(lines) == 0 {
					// First line: handle special commands.
					switch line {
					case ":quit", ":exit":
						return nil
					case ":help":
						fmt.Println("Type Python code. Press Enter twice on a blank line to execute a block. :quit to exit.")
						goto nextPrompt
					case "":
						// Blank first line; just show a new prompt.
						goto nextPrompt
					}
				}

				// Two consecutive blank lines end the block (same as IPython).
				if line == "" && prevBlank {
					// Drop the trailing blank lines from the block.
					for len(lines) > 0 && lines[len(lines)-1] == "" {
						lines = lines[:len(lines)-1]
					}
					break
				}

				prevBlank = line == ""
				lines = append(lines, line)
			}

			{
				code := strings.Join(lines, "\n")
				code = strings.TrimRight(code, "\n")
				if strings.TrimSpace(code) == "" {
					goto nextPrompt
				}

				outCh, err := kernel.Execute(ctx, code)
				if err != nil {
					fmt.Fprintf(os.Stderr, "error: %v\n", err)
					goto nextPrompt
				}
				printOutputs(outCh, outputDir)
			}

		nextPrompt:
		}
	},
}

func init() {
	execCmd.Flags().StringP("session", "s", "", "Endpoint ID of an existing runtime to reuse (from 'colabctl sessions')")
	execCmd.Flags().StringP("accelerator", "a", "", "Accelerator for a new runtime: T4, A100, TPU, L4, A100-80GB (default: CPU)")
	execCmd.Flags().StringP("output-dir", "o", "", "Directory to save binary outputs (images, audio, video) into (default: current directory)")
	rootCmd.AddCommand(execCmd)
}
