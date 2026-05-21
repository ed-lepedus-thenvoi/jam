package cli

import (
	"bytes"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func agentsServer(t *testing.T, wantKey, body string, status int) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/me/agents", func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("x-api-key") != wantKey {
			w.WriteHeader(http.StatusForbidden)
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

func TestAgentList_PrintsAgents(t *testing.T) {
	home := t.TempDir()
	srv := agentsServer(t, "band_u_test", `{
		"data": [
			{"id":"e051d58b-8b74-4999-880e-9f95c8fd1e96","name":"claude-gateway-builder","description":"Hermes gateway session"},
			{"id":"b1fdeff9-f037-498a-af8a-7cf490b25ad7","name":"claude-jam-cli-builder","description":"Jam CLI session"}
		],
		"metadata":{"page":1,"total_pages":1,"page_size":20,"total_count":2}
	}`, 200)
	writeConfig(t, home, srv.URL, "band_u_test")

	var stdout, stderr bytes.Buffer
	code := Execute([]string{"agent", "list"}, nil, &stdout, &stderr, Env{HomeDir: home})
	if code != 0 {
		t.Fatalf("exit %d\nstderr: %s", code, stderr.String())
	}
	out := stdout.String()
	for _, want := range []string{
		"claude-gateway-builder", "e051d58b",
		"claude-jam-cli-builder", "b1fdeff9",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("stdout missing %q\ngot:\n%s", want, out)
		}
	}
}

func TestAgentList_NoConfigErrors(t *testing.T) {
	home := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"agent", "list"}, nil, &stdout, &stderr, Env{HomeDir: home})
	if code == 0 {
		t.Fatalf("expected nonzero exit without config")
	}
	if !strings.Contains(stderr.String(), "jam init") {
		t.Errorf("expected stderr to hint at running 'jam init', got: %s", stderr.String())
	}
}
