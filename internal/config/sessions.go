package config

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"
)

// SessionState is the on-disk record for one active Colab runtime.
type SessionState struct {
	Endpoint       string    `json:"endpoint"`
	URL            string    `json:"url"`
	Token          string    `json:"token"`
	TokenExpiresAt time.Time `json:"tokenExpiresAt"`
	KernelID       string    `json:"kernelId,omitempty"`
}

// Valid returns true if the session's proxy token has not yet expired.
func (s *SessionState) Valid() bool {
	return s.Token != "" && time.Now().Before(s.TokenExpiresAt)
}

type sessionFile struct {
	Sessions map[string]*SessionState `json:"sessions"`
}

func sessionsPath() (string, error) {
	dir, err := Dir()
	if err != nil {
		return "", err
	}
	return filepath.Join(dir, "sessions.json"), nil
}

func loadSessionFile() (*sessionFile, error) {
	path, err := sessionsPath()
	if err != nil {
		return nil, err
	}
	f, err := os.Open(path)
	if errors.Is(err, os.ErrNotExist) {
		return &sessionFile{Sessions: map[string]*SessionState{}}, nil
	}
	if err != nil {
		return nil, err
	}
	defer f.Close()

	var sf sessionFile
	if err := json.NewDecoder(f).Decode(&sf); err != nil {
		return &sessionFile{Sessions: map[string]*SessionState{}}, nil
	}
	if sf.Sessions == nil {
		sf.Sessions = map[string]*SessionState{}
	}
	return &sf, nil
}

func saveSessionFile(sf *sessionFile) error {
	dir, err := Dir()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0700); err != nil {
		return err
	}
	path, err := sessionsPath()
	if err != nil {
		return err
	}
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
	if err != nil {
		return err
	}
	defer f.Close()
	return json.NewEncoder(f).Encode(sf)
}

// SaveSession persists a session under the given endpoint key.
func SaveSession(s *SessionState) error {
	sf, err := loadSessionFile()
	if err != nil {
		return err
	}
	sf.Sessions[s.Endpoint] = s
	return saveSessionFile(sf)
}

// LoadSession returns the saved session for the given endpoint, or nil if not
// found or the token has expired.
func LoadSession(endpoint string) (*SessionState, error) {
	sf, err := loadSessionFile()
	if err != nil {
		return nil, err
	}
	s, ok := sf.Sessions[endpoint]
	if !ok {
		return nil, nil
	}
	return s, nil
}

// ListSessions returns all saved sessions regardless of token validity.
func ListSessions() ([]*SessionState, error) {
	sf, err := loadSessionFile()
	if err != nil {
		return nil, err
	}
	out := make([]*SessionState, 0, len(sf.Sessions))
	for _, s := range sf.Sessions {
		out = append(out, s)
	}
	return out, nil
}

// DeleteSession removes a saved session by endpoint.
func DeleteSession(endpoint string) error {
	sf, err := loadSessionFile()
	if err != nil {
		return err
	}
	delete(sf.Sessions, endpoint)
	return saveSessionFile(sf)
}
