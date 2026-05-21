package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/spf13/cobra"
)

// twoProfileServer responds to /api/v1/me/profile with one of two payloads
// depending on which key the request carries. Lets us tell which profile
// jam picked just by reading the output.
func twoProfileServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/me/profile", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Header.Get("x-api-key") {
		case "band_u_PROD":
			_, _ = w.Write([]byte(`{"data":{"id":"u","email":"prod@example.com","first_name":"Prod","last_name":"User","role":"user"}}`))
		case "band_u_STAGING":
			_, _ = w.Write([]byte(`{"data":{"id":"u","email":"staging@example.com","first_name":"Staging","last_name":"User","role":"user"}}`))
		default:
			w.WriteHeader(http.StatusForbidden)
		}
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return srv
}

func TestProfile_InitWritesToNamedProfilePath(t *testing.T) {
	home := t.TempDir()
	srv := twoProfileServer(t)

	var out, errb bytes.Buffer
	code := Execute(
		[]string{"init", "--profile", "staging", "--base-url", srv.URL, "--user-api-key", "band_u_STAGING"},
		nil, &out, &errb,
		Env{HomeDir: home, Getenv: func(string) string { return "" }},
	)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errb.String())
	}

	staging := filepath.Join(home, ".config", "jam", "profiles", "staging.json")
	if _, err := os.Stat(staging); err != nil {
		t.Errorf("staging profile not written: %v", err)
	}
	defaultProfile := filepath.Join(home, ".config", "jam", "profiles", "default.json")
	if _, err := os.Stat(defaultProfile); !os.IsNotExist(err) {
		t.Errorf("default profile should not exist; got err=%v", err)
	}
}

func TestProfile_WhoamiPicksProfileFromEnv(t *testing.T) {
	home := t.TempDir()
	srv := twoProfileServer(t)
	writeProfileConfig(t, home, "default", srv.URL, "band_u_PROD")
	writeProfileConfig(t, home, "staging", srv.URL, "band_u_STAGING")

	getenv := func(k string) string {
		if k == "JAM_PROFILE" {
			return "staging"
		}
		return ""
	}
	var out, errb bytes.Buffer
	code := Execute([]string{"whoami"}, nil, &out, &errb, Env{HomeDir: home, Getenv: getenv})
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "staging@example.com") {
		t.Errorf("expected staging profile to be used; got: %s", out.String())
	}
}

func TestProfile_FlagOverridesEnv(t *testing.T) {
	home := t.TempDir()
	srv := twoProfileServer(t)
	writeProfileConfig(t, home, "default", srv.URL, "band_u_PROD")
	writeProfileConfig(t, home, "staging", srv.URL, "band_u_STAGING")

	getenv := func(k string) string {
		if k == "JAM_PROFILE" {
			return "staging"
		}
		return ""
	}
	var out, errb bytes.Buffer
	code := Execute([]string{"--profile", "default", "whoami"}, nil, &out, &errb,
		Env{HomeDir: home, Getenv: getenv})
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, errb.String())
	}
	if !strings.Contains(out.String(), "prod@example.com") {
		t.Errorf("expected --profile default to win over JAM_PROFILE=staging; got: %s", out.String())
	}
}

func TestInit_DefaultBaseURLIsAppBandAi(t *testing.T) {
	env := Env{HomeDir: t.TempDir(), Getenv: func(string) string { return "" }}
	root := newRootCmd(nil, &bytes.Buffer{}, &bytes.Buffer{}, env)
	var initCmd *cobra.Command
	for _, c := range root.Commands() {
		if c.Use == "init" {
			initCmd = c
			break
		}
	}
	if initCmd == nil {
		t.Fatal("init command not found on root")
	}
	flag := initCmd.Flags().Lookup("base-url")
	if flag == nil {
		t.Fatal("--base-url flag missing")
	}
	if flag.DefValue != "https://app.band.ai" {
		t.Errorf("default base-url = %q, want https://app.band.ai", flag.DefValue)
	}
}
