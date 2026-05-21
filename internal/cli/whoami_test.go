package cli

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeConfig(t *testing.T, home, baseURL, apiKey string) {
	t.Helper()
	dir := filepath.Join(home, ".config", "jam")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	body := `{"base_url":"` + baseURL + `","user_api_key":"` + apiKey + `"}`
	if err := os.WriteFile(filepath.Join(dir, "config.json"), []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestWhoami_PrintsProfile(t *testing.T) {
	home := t.TempDir()
	srv := profileServer(t, "band_u_test",
		`{"data":{"id":"u-1","email":"ed@example.com","first_name":"Ed","last_name":"Lepedus","role":"user","listed_in_directory":false}}`,
		200,
	)
	writeConfig(t, home, srv.URL, "band_u_test")

	var stdout, stderr bytes.Buffer
	code := Execute([]string{"whoami"}, nil, &stdout, &stderr, Env{HomeDir: home})
	if code != 0 {
		t.Fatalf("exit %d\nstderr: %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{"Ed Lepedus", "ed@example.com"} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q\ngot: %s", want, out)
		}
	}
}

func TestWhoami_NoConfigErrors(t *testing.T) {
	home := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"whoami"}, nil, &stdout, &stderr, Env{HomeDir: home})
	if code == 0 {
		t.Fatalf("expected nonzero exit without config")
	}
	if !strings.Contains(stderr.String(), "jam init") {
		t.Errorf("expected stderr to hint at running 'jam init', got: %s", stderr.String())
	}
}
