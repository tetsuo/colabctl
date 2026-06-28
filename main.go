package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"
)

var (
	clientID     = ""
	clientSecret = ""
)

var rootCmd = &cobra.Command{
	Use: "colabctl",
}

func main() {
	if err := rootCmd.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func init() {
	// Allow credentials to be provided via environment variables as a
	// convenient alternative to build-time injection.
	if id := os.Getenv("COLAB_CLIENT_ID"); id != "" {
		clientID = id
	}
	if secret := os.Getenv("COLAB_CLIENT_SECRET"); secret != "" {
		clientSecret = secret
	}
}

// requireCredentials prints a helpful error and exits if the OAuth2 client
// credentials have not been configured.
func requireCredentials() {
	if clientID == "" || clientSecret == "" {
		fmt.Fprintln(os.Stderr, `Error: OAuth2 credentials not set.

Set COLAB_CLIENT_ID and COLAB_CLIENT_SECRET environment variables, or run:

  colabctl auth login --client-id <id> --client-secret <secret>

To create credentials, visit:
  https://console.cloud.google.com/apis/credentials`)
		os.Exit(1)
	}
}
