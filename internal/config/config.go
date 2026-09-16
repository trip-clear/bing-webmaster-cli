// Package config resolves the Bing Webmaster API key and the default site.
//
// The API has one credential -- an API key issued per user in the Bing
// Webmaster Tools UI -- and it is read from, in priority order:
//
//  1. --api-key
//  2. BWT_API_KEY, then BING_WEBMASTER_API_KEY
//  3. ~/.config/bwt/config.json, written by `bwt auth set`
package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// ErrNoAPIKey is returned when no key can be resolved.
var ErrNoAPIKey = errors.New(`API キーが見つかりません

Bing Webmaster Tools（https://www.bing.com/webmasters）にログインし、
「設定 > API アクセス > API キー」でキーを発行してから、次のいずれかを設定してください:

  1. bwt auth set            対話的に入力して ~/.config/bwt/config.json に保存
  2. export BWT_API_KEY=...  環境変数（CI 向け）

設定後は bwt auth status で確認できます。`)

// Source describes where a credential came from, for `auth status`.
type Source struct {
	Kind   string // "flag", "env", "file"
	Detail string
}

// File is the on-disk config.
type File struct {
	APIKey string `json:"api_key"`
	Site   string `json:"site,omitempty"`
}

// Dir is where the config lives. BWT_CONFIG_DIR overrides it, which is how the
// tests keep out of a developer's real config.
func Dir() string {
	if d := os.Getenv("BWT_CONFIG_DIR"); d != "" {
		return d
	}
	if d := os.Getenv("XDG_CONFIG_HOME"); d != "" {
		return filepath.Join(d, "bwt")
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return ".bwt"
	}
	return filepath.Join(home, ".config", "bwt")
}

// Path is the config file's location.
func Path() string { return filepath.Join(Dir(), "config.json") }

// Load reads the config file. A missing file is not an error.
func Load() (File, error) {
	var f File
	data, err := os.ReadFile(Path())
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return f, nil
		}
		return f, err
	}
	if err := json.Unmarshal(data, &f); err != nil {
		return f, fmt.Errorf("%s を読めません: %w（削除して bwt auth set をやり直してください）", Path(), err)
	}
	return f, nil
}

// Save writes the config file with owner-only permissions: it holds a live
// credential.
func Save(f File) error {
	if err := os.MkdirAll(Dir(), 0o700); err != nil {
		return err
	}
	data, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(Path(), append(data, '\n'), 0o600)
}

// APIKey resolves the key, preferring the flag, then the environment, then the
// config file.
func APIKey(flag string) (string, Source, error) {
	if k := strings.TrimSpace(flag); k != "" {
		return k, Source{Kind: "flag", Detail: "--api-key"}, nil
	}
	for _, name := range []string{"BWT_API_KEY", "BING_WEBMASTER_API_KEY"} {
		if k := strings.TrimSpace(os.Getenv(name)); k != "" {
			return k, Source{Kind: "env", Detail: name}, nil
		}
	}
	f, err := Load()
	if err != nil {
		return "", Source{}, err
	}
	if k := strings.TrimSpace(f.APIKey); k != "" {
		return k, Source{Kind: "file", Detail: Path()}, nil
	}
	return "", Source{}, ErrNoAPIKey
}

// Site resolves the default property from --site, BWT_SITE, or the config file.
func Site(flag string) string {
	if s := strings.TrimSpace(flag); s != "" {
		return s
	}
	if s := strings.TrimSpace(os.Getenv("BWT_SITE")); s != "" {
		return s
	}
	f, err := Load()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(f.Site)
}

// Redact shows enough of a key to tell two apart without printing a usable one.
func Redact(key string) string {
	if len(key) <= 8 {
		return strings.Repeat("*", len(key))
	}
	return key[:4] + strings.Repeat("*", len(key)-8) + key[len(key)-4:]
}

// Logout removes the stored key, leaving any other settings in place. It
// reports whether a key was actually there.
func Logout() (string, bool, error) {
	f, err := Load()
	if err != nil {
		return Path(), false, err
	}
	if f.APIKey == "" {
		return Path(), false, nil
	}
	f.APIKey = ""
	return Path(), true, Save(f)
}
