package config

import (
	"os"
	"strings"
	"testing"
)

func TestAPIKeyPrecedence(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("BWT_CONFIG_DIR", dir)
	t.Setenv("BWT_API_KEY", "")
	t.Setenv("BING_WEBMASTER_API_KEY", "")

	if err := Save(File{APIKey: "from-file"}); err != nil {
		t.Fatal(err)
	}

	key, src, err := APIKey("")
	if err != nil {
		t.Fatal(err)
	}
	if key != "from-file" || src.Kind != "file" {
		t.Errorf("got %q from %s, want the stored key", key, src.Kind)
	}

	t.Setenv("BING_WEBMASTER_API_KEY", "from-env")
	if key, src, _ = APIKey(""); key != "from-env" || src.Kind != "env" {
		t.Errorf("got %q from %s, want the environment to win over the file", key, src.Kind)
	}

	if key, src, _ = APIKey("from-flag"); key != "from-flag" || src.Kind != "flag" {
		t.Errorf("got %q from %s, want the flag to win over everything", key, src.Kind)
	}
}

func TestAPIKeyMissing(t *testing.T) {
	t.Setenv("BWT_CONFIG_DIR", t.TempDir())
	t.Setenv("BWT_API_KEY", "")
	t.Setenv("BING_WEBMASTER_API_KEY", "")

	if _, _, err := APIKey(""); err != ErrNoAPIKey {
		t.Fatalf("got %v, want ErrNoAPIKey so the CLI can exit with its own code", err)
	}
}

// The file holds a live credential, so it must not be world-readable.
func TestSaveIsOwnerOnly(t *testing.T) {
	t.Setenv("BWT_CONFIG_DIR", t.TempDir())
	if err := Save(File{APIKey: "secret"}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(Path())
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Errorf("mode = %o, want 600", perm)
	}
}

// Logout must not throw away the rest of the config.
func TestLogoutKeepsOtherSettings(t *testing.T) {
	t.Setenv("BWT_CONFIG_DIR", t.TempDir())
	if err := Save(File{APIKey: "secret", Site: "https://example.com/"}); err != nil {
		t.Fatal(err)
	}

	if _, had, err := Logout(); err != nil || !had {
		t.Fatalf("Logout() = %v, %v; want it to report the key was there", had, err)
	}
	f, err := Load()
	if err != nil {
		t.Fatal(err)
	}
	if f.APIKey != "" || f.Site != "https://example.com/" {
		t.Errorf("after logout: %+v, want only the key cleared", f)
	}

	// A second logout is not an error.
	if _, had, err := Logout(); err != nil || had {
		t.Errorf("second Logout() = %v, %v; want (false, nil)", had, err)
	}
}

func TestRedactKeepsEndsOnly(t *testing.T) {
	got := Redact("abcd1234efgh")
	if !strings.HasPrefix(got, "abcd") || !strings.HasSuffix(got, "efgh") || strings.Contains(got, "1234") {
		t.Errorf("Redact = %q, want the middle hidden", got)
	}
	if Redact("short") != "*****" {
		t.Errorf("a short key must be hidden entirely, got %q", Redact("short"))
	}
}

func TestLoadMissingFileIsNotAnError(t *testing.T) {
	t.Setenv("BWT_CONFIG_DIR", t.TempDir())
	f, err := Load()
	if err != nil || f.APIKey != "" {
		t.Fatalf("got %+v, %v; want an empty config and no error", f, err)
	}
}
