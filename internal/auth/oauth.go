// Package auth handles Google OAuth2 for the CLI using a local loopback server
// to receive the authorization code.
package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"time"

	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"

	"github.com/tetsuo/colabctl/internal/config"
)

// Scopes required to use the Drive API and the Colab runtime API.
// The colaboratory scope is mandatory; without it the /tun/m/assign
// endpoint returns 403 SCOPE_NOT_PERMITTED.
var scopes = []string{
	"openid",
	"https://www.googleapis.com/auth/userinfo.profile",
	"https://www.googleapis.com/auth/userinfo.email",
	"https://www.googleapis.com/auth/cloud-platform",
	"https://www.googleapis.com/auth/colaboratory",
	"https://www.googleapis.com/auth/drive",
}

// oauthConfig builds an oauth2.Config from the supplied client credentials.
func oauthConfig(clientID, clientSecret, redirectURL string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     clientID,
		ClientSecret: clientSecret,
		RedirectURL:  redirectURL,
		Scopes:       scopes,
		Endpoint:     google.Endpoint,
	}
}

// Login performs an interactive OAuth2 flow. It starts a temporary local HTTP
// server on a free port, opens the browser for the user, waits for the
// authorization code, exchanges it for tokens, and persists them to disk.
func Login(ctx context.Context, clientID, clientSecret string) error {
	// Pick a free port on localhost.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("start local server: %w", err)
	}
	port := listener.Addr().(*net.TCPAddr).Port
	redirectURL := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	cfg := oauthConfig(clientID, clientSecret, redirectURL)

	// Generate a random state value to guard against CSRF.
	state, err := randomState()
	if err != nil {
		return err
	}

	authURL := cfg.AuthCodeURL(state, oauth2.AccessTypeOffline, oauth2.ApprovalForce)

	codeCh := make(chan string, 1)
	errCh := make(chan error, 1)

	mux := http.NewServeMux()
	srv := &http.Server{Handler: mux}

	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("state") != state {
			http.Error(w, "state mismatch", http.StatusBadRequest)
			errCh <- errors.New("oauth2 state mismatch; possible CSRF")
			return
		}
		code := r.URL.Query().Get("code")
		if code == "" {
			msg := r.URL.Query().Get("error")
			http.Error(w, "authorization denied", http.StatusBadRequest)
			errCh <- fmt.Errorf("authorization denied: %s", msg)
			return
		}
		fmt.Fprintln(w, "<html><body><h2>Authentication successful; you can close this tab.</h2></body></html>")
		codeCh <- code
	})

	go func() {
		if err := srv.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	fmt.Printf("Opening browser for authentication.\nIf the browser does not open, visit:\n\n  %s\n\n", authURL)
	openBrowser(authURL)

	var code string
	select {
	case code = <-codeCh:
	case err = <-errCh:
		_ = srv.Shutdown(context.Background())
		return err
	case <-time.After(5 * time.Minute):
		_ = srv.Shutdown(context.Background())
		return errors.New("timed out waiting for authorization")
	}
	_ = srv.Shutdown(context.Background())

	tok, err := cfg.Exchange(ctx, code)
	if err != nil {
		return fmt.Errorf("exchange code: %w", err)
	}
	return config.SaveToken(tok)
}

// TokenSource loads the stored token and wraps it in an auto-refreshing
// TokenSource. Returns an error if no token has been saved (i.e. the user has
// not yet run auth login).
func TokenSource(ctx context.Context, clientID, clientSecret string) (oauth2.TokenSource, error) {
	tok, err := config.LoadToken()
	if err != nil {
		return nil, fmt.Errorf("not authenticated; run \"colabctl auth login\" first: %w", err)
	}
	cfg := oauthConfig(clientID, clientSecret, "")
	return cfg.TokenSource(ctx, tok), nil
}

// Client returns an *http.Client backed by the stored token.
func Client(ctx context.Context, clientID, clientSecret string) (*http.Client, error) {
	ts, err := TokenSource(ctx, clientID, clientSecret)
	if err != nil {
		return nil, err
	}
	return oauth2.NewClient(ctx, ts), nil
}

func randomState() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", fmt.Errorf("generate state: %w", err)
	}
	return base64.URLEncoding.EncodeToString(b), nil
}

func openBrowser(url string) {
	var cmd string
	var args []string
	switch runtime.GOOS {
	case "darwin":
		cmd = "open"
		args = []string{url}
	case "windows":
		cmd = "rundll32"
		args = []string{"url.dll,FileProtocolHandler", url}
	default:
		cmd = "xdg-open"
		args = []string{url}
	}
	_ = exec.Command(cmd, args...).Start()
}
