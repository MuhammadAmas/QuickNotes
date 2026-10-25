// Package auth signs in to QuickNotes as the user: it finds a saved Google session
// (the Mac app's session.json, or the server's own file from `quicknotes-mcp login`),
// exchanges its refresh token for a short-lived ID token, and keeps that only in memory.
//
// Tokens are never logged or put in error messages. The Mac app's session file is
// only ever read, never written, moved or deleted.
package auth

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// Config is the public Firebase web configuration the apps also use.
type Config struct {
	APIKey    string
	ProjectID string
	WebURL    string // the hosted web app, e.g. https://quicknotes-hq.web.app (for the /link sign-in page)
}

// LoadConfig reads QUICKNOTES_API_KEY, QUICKNOTES_PROJECT_ID and QUICKNOTES_WEB_URL, falling back
// to the private quicknotes.config.json named by QUICKNOTES_CONFIG (or found two folders above the
// executable, i.e. the repository root when the binary is mcp/bin/quicknotes-mcp).
func LoadConfig(getenv func(string) string, executable string) (Config, error) {
	cfg := Config{APIKey: getenv("QUICKNOTES_API_KEY"), ProjectID: getenv("QUICKNOTES_PROJECT_ID"), WebURL: getenv("QUICKNOTES_WEB_URL")}
	if cfg.APIKey != "" && cfg.ProjectID != "" {
		return cfg, nil
	}
	path := getenv("QUICKNOTES_CONFIG")
	if path == "" && executable != "" {
		path = filepath.Join(filepath.Dir(executable), "..", "..", "quicknotes.config.json")
	}
	if path == "" {
		return Config{}, errors.New("QuickNotes settings not found: set QUICKNOTES_CONFIG to your quicknotes.config.json")
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("QuickNotes settings not found at %s: set QUICKNOTES_CONFIG to your quicknotes.config.json", path)
	}
	var file struct {
		Firebase struct {
			APIKey    string `json:"apiKey"`
			ProjectID string `json:"projectId"`
		} `json:"firebase"`
		WebURL string `json:"webURL"`
	}
	if err := json.Unmarshal(raw, &file); err != nil {
		return Config{}, fmt.Errorf("%s is not valid JSON", path)
	}
	if cfg.APIKey == "" {
		cfg.APIKey = file.Firebase.APIKey
	}
	if cfg.ProjectID == "" {
		cfg.ProjectID = file.Firebase.ProjectID
	}
	if cfg.WebURL == "" {
		cfg.WebURL = file.WebURL
	}
	if cfg.APIKey == "" || cfg.ProjectID == "" {
		return Config{}, fmt.Errorf("%s has no firebase.apiKey / firebase.projectId", path)
	}
	return cfg, nil
}
