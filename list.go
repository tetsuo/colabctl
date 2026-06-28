package main

import (
	"fmt"
	"os"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/tetsuo/colabctl/internal/auth"
	"github.com/tetsuo/colabctl/internal/drive"
)

var listCmd = &cobra.Command{
	Use:     "list",
	Aliases: []string{"ls"},
	Short:   "List Colab notebooks from Google Drive",
	RunE: func(cmd *cobra.Command, _ []string) error {
		requireCredentials()

		httpClient, err := auth.Client(cmd.Context(), clientID, clientSecret)
		if err != nil {
			return err
		}

		driveClient, err := drive.New(cmd.Context(), httpClient)
		if err != nil {
			return err
		}

		notebooks, err := driveClient.ListNotebooks(cmd.Context())
		if err != nil {
			return err
		}

		if len(notebooks) == 0 {
			fmt.Println("No Colab notebooks found in your Drive.")
			return nil
		}

		w := tabwriter.NewWriter(os.Stdout, 0, 0, 2, ' ', 0)
		fmt.Fprintln(w, "ID\tNAME\tMODIFIED")
		fmt.Fprintln(w, "--\t----\t--------")
		for _, nb := range notebooks {
			modified := nb.Modified
			if t, err := time.Parse(time.RFC3339, nb.Modified); err == nil {
				modified = t.Format("2006-01-02 15:04")
			}
			fmt.Fprintf(w, "%s\t%s\t%s\n", nb.ID, nb.Name, modified)
		}
		return w.Flush()
	},
}

func init() {
	rootCmd.AddCommand(listCmd)
}
