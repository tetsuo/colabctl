package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"

	"golang.org/x/oauth2"
)

// Dir returns the directory used to store all persistent state.
func Dir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, ".config", "colab"), nil
}

// TokenPath returns the path to the stored OAuth2 token file.
func TokenPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "token.json"), nil
}

// LoadToken reads the stored token from disk. Returns an error wrapping
// os.ErrNotExist when no token has been saved yet.
func LoadToken() (*oauth2.Token, error) {
	path, err := TokenPath()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var tok oauth2.Token
	if err := json.NewDecoder(f).Decode(&tok); err != nil {
		return nil, err
	}
	return &tok, nil
}

// SaveToken writes the token to disk, creating parent directories as needed.
func SaveToken(tok *oauth2.Token) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	path := filepath.Join(dir, "token.json")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(tok)
}

// DeleteToken removes the stored token, effectively logging out.
func DeleteToken() error {
	path, err := TokenPath()
	if err != nil {
		return err
	}
	err = os.Remove(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
