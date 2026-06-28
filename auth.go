package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/tetsuo/colabctl/internal/auth"
	"github.com/tetsuo/colabctl/internal/config"
)

var authCmd = &cobra.Command{
	Use:   "auth",
	Short: "Manage authentication",
}

var loginCmd = &cobra.Command{
	Use:   "login",
	Short: "Log in to Google and store credentials locally",
	RunE: func(cmd *cobra.Command, _ []string) error {
		id, _ := cmd.Flags().GetString("client-id")
		secret, _ := cmd.Flags().GetString("client-secret")

		if id != "" {
			clientID = id
		}
		if secret != "" {
			clientSecret = secret
		}
		requireCredentials()

		if err := auth.Login(cmd.Context(), clientID, clientSecret); err != nil {
			return err
		}
		fmt.Println("Authentication successful. Token saved.")
		return nil
	},
}

var logoutCmd = &cobra.Command{
	Use:   "logout",
	Short: "Remove locally stored credentials",
	RunE: func(_ *cobra.Command, _ []string) error {
		if err := config.DeleteToken(); err != nil {
			return err
		}
		fmt.Println("Logged out.")
		return nil
	},
}

var authStatusCmd = &cobra.Command{
	Use:   "status",
	Short: "Show whether you are currently authenticated",
	RunE: func(cmd *cobra.Command, _ []string) error {
		tok, err := config.LoadToken()
		if err != nil {
			fmt.Fprintln(os.Stderr, "Not authenticated. Run `colabctl auth login`.")
			return nil
		}
		if tok.Valid() {
			fmt.Println("Authenticated. Token is valid.")
		} else {
			fmt.Println("Authenticated (token expired, will be refreshed automatically).")
		}
		return nil
	},
}

func init() {
	loginCmd.Flags().String("client-id", "", "Google OAuth2 client ID (overrides COLAB_CLIENT_ID)")
	loginCmd.Flags().String("client-secret", "", "Google OAuth2 client secret (overrides COLAB_CLIENT_SECRET)")

	authCmd.AddCommand(loginCmd)
	authCmd.AddCommand(logoutCmd)
	authCmd.AddCommand(authStatusCmd)
	rootCmd.AddCommand(authCmd)
}
