package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/thenvoi/jam/internal/session"
)

type chatHarness struct {
	home              string
	cwd               string
	srv               *httptest.Server
	createCalls       atomic.Int32
	participantCalls  atomic.Int32
	lastCreatedID     atomic.Value // string
	lastParticipantID atomic.Value // string
}

func newChatHarness(t *testing.T) *chatHarness {
	t.Helper()
	h := &chatHarness{
		home: t.TempDir(),
		cwd:  t.TempDir(),
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/agent/peers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"peer-bob","name":"bob","handle":"alice/bob","type":"Agent"},
			{"id":"peer-carol","name":"carol","handle":"alice/carol","type":"User"}
		]}`))
	})
	mux.HandleFunc("/api/v1/agent/chats", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.Method {
		case "POST":
			h.createCalls.Add(1)
			id := "new-chat-id"
			h.lastCreatedID.Store(id)
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"data":{"id":"` + id + `","title":null,"task_id":null,"inserted_at":"2026-05-21T00:00:00Z","updated_at":"2026-05-21T00:00:00Z"}}`))
		case "GET":
			_, _ = w.Write([]byte(`{"data":[
				{"id":"chat-A","title":"Onboarding","task_id":null,"inserted_at":"2026-05-21T10:00:00Z","updated_at":"2026-05-21T12:00:00Z"},
				{"id":"chat-B","title":null,"task_id":null,"inserted_at":"2026-05-21T11:00:00Z","updated_at":"2026-05-21T11:30:00Z"}
			],"metadata":{"page":1,"total_pages":1,"page_size":20,"total_count":2}}`))
		default:
			w.WriteHeader(405)
		}
	})
	mux.HandleFunc("/api/v1/agent/chats/", func(w http.ResponseWriter, r *http.Request) {
		// /api/v1/agent/chats/<id>/participants
		if strings.HasSuffix(r.URL.Path, "/participants") && r.Method == "POST" {
			h.participantCalls.Add(1)
			body, _ := io.ReadAll(r.Body)
			var parsed struct {
				Participant struct {
					ParticipantID string `json:"participant_id"`
				} `json:"participant"`
			}
			_ = json.Unmarshal(body, &parsed)
			h.lastParticipantID.Store(parsed.Participant.ParticipantID)
			w.WriteHeader(201)
			_, _ = w.Write([]byte(`{"data":{"id":"` + parsed.Participant.ParticipantID + `","status":"inactive","type":"Agent","role":"member"}}`))
			return
		}
		w.WriteHeader(404)
	})
	h.srv = httptest.NewServer(mux)
	t.Cleanup(h.srv.Close)
	writeConfig(t, h.home, h.srv.URL, "band_u_test")
	scope := session.Scope(h.cwd)
	if err := session.Save(h.home, "", &session.State{
		Scope: scope, Cwd: h.cwd, AgentID: "self", AgentName: "self",
		AgentAPIKey: "band_a_SELF", Handle: "alice/self", PID: 1,
	}); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *chatHarness) env() Env {
	return Env{HomeDir: h.home, Cwd: h.cwd, Getenv: func(string) string { return "" }}
}

func TestChatNew_CreatesAndPrintsID(t *testing.T) {
	h := newChatHarness(t)
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"chat", "new"}, nil, &stdout, &stderr, h.env())
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stderr.String())
	}
	if h.createCalls.Load() != 1 {
		t.Errorf("expected 1 create call, got %d", h.createCalls.Load())
	}
	if !strings.Contains(stdout.String(), "new-chat-id") {
		t.Errorf("expected stdout to print chat id, got: %s", stdout.String())
	}
}

func TestChatNew_WithParticipantsAddsAll(t *testing.T) {
	h := newChatHarness(t)
	var stdout, stderr bytes.Buffer
	code := Execute(
		[]string{"chat", "new", "--with", "alice/bob", "--with", "alice/carol"},
		nil, &stdout, &stderr, h.env(),
	)
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stderr.String())
	}
	if got := h.participantCalls.Load(); got != 2 {
		t.Errorf("expected 2 participant adds, got %d", got)
	}
	// Output should mention both handles
	for _, want := range []string{"alice/bob", "alice/carol", "new-chat-id"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("stdout missing %q\n%s", want, stdout.String())
		}
	}
}

func TestChatList_PrintsChats(t *testing.T) {
	h := newChatHarness(t)
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"chat", "list"}, nil, &stdout, &stderr, h.env())
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stderr.String())
	}
	for _, want := range []string{"chat-A", "Onboarding", "chat-B"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("chat list output missing %q\n%s", want, stdout.String())
		}
	}
}

func TestChatAdd_AddsParticipantsByHandle(t *testing.T) {
	h := newChatHarness(t)
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"chat", "add", "chat-X", "alice/bob"}, nil, &stdout, &stderr, h.env())
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stderr.String())
	}
	if got := h.participantCalls.Load(); got != 1 {
		t.Errorf("expected 1 participant add, got %d", got)
	}
	if h.lastParticipantID.Load().(string) != "peer-bob" {
		t.Errorf("expected peer-bob to be added, got %v", h.lastParticipantID.Load())
	}
}

func TestChatAdd_ErrorsForUnknownHandle(t *testing.T) {
	h := newChatHarness(t)
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"chat", "add", "chat-X", "alice/ghost"}, nil, &stdout, &stderr, h.env())
	if code == 0 {
		t.Fatalf("expected nonzero exit for unknown handle")
	}
	if !strings.Contains(stderr.String(), "ghost") {
		t.Errorf("expected error to name missing handle, got: %s", stderr.String())
	}
}
