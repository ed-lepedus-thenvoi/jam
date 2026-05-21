package cli

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// profileServer returns an httptest.Server that responds to /api/v1/me/profile.
// If wantKey is non-empty, requests without that x-api-key header get a 403.
func profileServer(t *testing.T, wantKey string, body string, status int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/me/profile", func(w http.ResponseWriter, r *http.Request) {
		if wantKey != "" && r.Header.Get("x-api-key") != wantKey {
			w.WriteHeader(http.StatusForbidden)
			_, _ = w.Write([]byte(`{"error":{"code":"forbidden"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestInit_WritesConfigAndReportsSuccess(t *testing.T) {
	home := t.TempDir()
	srv := profileServer(t, "band_u_test_KEY",
		`{"data":{"id":"u-1","email":"ed@example.com","first_name":"Ed","last_name":"Lepedus","role":"user","listed_in_directory":false}}`,
		200,
	)

	var stdout, stderr bytes.Buffer
	code := Execute(
		[]string{"init", "--base-url", srv.URL, "--user-api-key", "band_u_test_KEY"},
		nil, &stdout, &stderr,
		Env{HomeDir: home},
	)
	if code != 0 {
		t.Fatalf("exit %d\nstderr: %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "Ed Lepedus") {
		t.Errorf("expected stdout to mention authenticated user, got: %s", stdout.String())
	}

	cfgPath := filepath.Join(home, ".config", "jam", "profiles", "default.json")
	info, err := os.Stat(cfgPath)
	if err != nil {
		t.Fatalf("config file not written: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o600 {
		t.Errorf("expected config perms 0600, got %o", mode)
	}

	data, _ := os.ReadFile(cfgPath)
	var cfg map[string]any
	if err := json.Unmarshal(data, &cfg); err != nil {
		t.Fatalf("config not valid JSON: %v\n%s", err, data)
	}
	if cfg["base_url"] != srv.URL {
		t.Errorf("base_url = %v, want %s", cfg["base_url"], srv.URL)
	}
	if cfg["user_api_key"] != "band_u_test_KEY" {
		t.Errorf("user_api_key = %v, want band_u_test_KEY", cfg["user_api_key"])
	}
}

func TestInit_RejectsBadKey(t *testing.T) {
	home := t.TempDir()
	srv := profileServer(t, "band_u_real",
		`{"error":{"code":"forbidden"}}`, 403,
	)

	var stdout, stderr bytes.Buffer
	code := Execute(
		[]string{"init", "--base-url", srv.URL, "--user-api-key", "band_u_WRONG"},
		nil, &stdout, &stderr,
		Env{HomeDir: home},
	)
	if code == 0 {
		t.Fatalf("expected nonzero exit on bad key\nstdout: %s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(home, ".config", "jam", "profiles", "default.json")); !os.IsNotExist(err) {
		t.Errorf("config file should NOT exist after failed auth, got err=%v", err)
	}
	if strings.TrimSpace(stderr.String()) == "" {
		t.Errorf("expected explanation on stderr")
	}
}

func TestInit_IsIdempotentlyOverwrites(t *testing.T) {
	home := t.TempDir()
	srv := profileServer(t, "band_u_new",
		`{"data":{"id":"u-1","email":"ed@example.com","first_name":"Ed","last_name":"Lepedus","role":"user","listed_in_directory":false}}`,
		200,
	)
	cfgPath := filepath.Join(home, ".config", "jam", "profiles", "default.json")
	if err := os.MkdirAll(filepath.Dir(cfgPath), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfgPath, []byte(`{"base_url":"https://old","user_api_key":"band_u_old"}`), 0o600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Execute(
		[]string{"init", "--base-url", srv.URL, "--user-api-key", "band_u_new"},
		nil, &stdout, &stderr,
		Env{HomeDir: home},
	)
	if code != 0 {
		t.Fatalf("exit %d\nstderr: %s", code, stderr.String())
	}
	data, _ := os.ReadFile(cfgPath)
	if !strings.Contains(string(data), "band_u_new") {
		t.Errorf("config not overwritten with new key: %s", data)
	}
}
