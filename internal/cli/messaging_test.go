package cli

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ed-lepedus-thenvoi/jam/internal/inbox"
	"github.com/ed-lepedus-thenvoi/jam/internal/session"
)

// msgHarness wires a mock Band server (with /agent/peers + chat send/processed
// routes) and writes session state + a sample inbox file. Returns the env and
// a captured-bodies map so tests can assert on request shapes.
type msgHarness struct {
	home          string
	cwd           string
	teamName      string
	teammateName  string
	srv           *httptest.Server
	sendBody      atomic.Value // last POST /messages body
	processedHits atomic.Int32
}

func newMsgHarness(t *testing.T) *msgHarness {
	t.Helper()
	h := &msgHarness{
		home:         t.TempDir(),
		cwd:          t.TempDir(),
		teamName:     "test-team",
		teammateName: "team-lead",
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/agent/peers", func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"data":[
			{"id":"peer-bob","name":"bob","handle":"alice/bob","type":"Agent"},
			{"id":"peer-carol","name":"carol","handle":"alice/carol","type":"User"}
		]}`))
	})
	mux.HandleFunc("/api/v1/agent/chats/", func(w http.ResponseWriter, r *http.Request) {
		// /api/v1/agent/chats/<id>/messages OR /api/v1/agent/chats/<id>/messages/<msg_id>/processed
		path := strings.TrimPrefix(r.URL.Path, "/api/v1/agent/chats/")
		switch {
		case strings.HasSuffix(path, "/processed"):
			h.processedHits.Add(1)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"id":"x","status":"processed","success":true}}`))
		case strings.HasSuffix(path, "/messages"):
			body, _ := io.ReadAll(r.Body)
			h.sendBody.Store(body)
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"id":"sent-msg-id","success":true}}`))
		default:
			w.WriteHeader(404)
		}
	})
	h.srv = httptest.NewServer(mux)
	t.Cleanup(h.srv.Close)

	writeConfig(t, h.home, h.srv.URL, "band_u_test")
	// Pre-write a session as if onboard had completed.
	scope := session.Scope(h.cwd)
	st := &session.State{
		Scope:        scope,
		Cwd:          h.cwd,
		AgentID:      "self-agent-id",
		AgentName:    "claude-self",
		AgentAPIKey:  "band_a_SELF",
		Handle:       "alice/claude-self",
		PID:          1, // never checked by messaging cmds; just non-zero
		LogPath:      "/dev/null",
		TeamName:     h.teamName,
		TeammateName: h.teammateName,
	}
	if err := session.Save(h.home, "", st); err != nil {
		t.Fatal(err)
	}
	return h
}

func (h *msgHarness) env() Env {
	return Env{HomeDir: h.home, Cwd: h.cwd, Getenv: func(string) string { return "" }}
}

func (h *msgHarness) writeInbox(t *testing.T, notes []inbox.Notification) {
	t.Helper()
	dir := filepath.Join(h.home, ".claude", "teams", h.teamName, "inboxes")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatal(err)
	}
	data, _ := json.Marshal(notes)
	if err := os.WriteFile(filepath.Join(dir, h.teammateName+".json"), data, 0o600); err != nil {
		t.Fatal(err)
	}
}

func (h *msgHarness) sentMessageBody(t *testing.T) map[string]any {
	t.Helper()
	v := h.sendBody.Load()
	if v == nil {
		t.Fatal("no message sent")
	}
	var out map[string]any
	if err := json.Unmarshal(v.([]byte), &out); err != nil {
		t.Fatal(err)
	}
	return out
}

func TestSend_ResolvesHandleAndPosts(t *testing.T) {
	h := newMsgHarness(t)
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"send", "chat-1", "@alice/bob hello there"}, nil, &stdout, &stderr, h.env())
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stderr.String())
	}
	body := h.sentMessageBody(t)
	msg := body["message"].(map[string]any)
	// Content text is preserved (full handle); the mention object carries
	// both handle (globally unique) and name (short), so the platform
	// substitutes the @owner/short text cleanly.
	if msg["content"] != "@alice/bob hello there" {
		t.Errorf("content = %v (expected text preserved)", msg["content"])
	}
	mentions := msg["mentions"].([]any)
	if len(mentions) != 1 {
		t.Fatalf("mentions = %v", mentions)
	}
	m := mentions[0].(map[string]any)
	if m["id"] != "peer-bob" || m["name"] != "bob" || m["handle"] != "alice/bob" {
		t.Errorf("mention = %v (expected id+name+handle all populated)", m)
	}
}

func TestSend_ErrorsWithoutMention(t *testing.T) {
	h := newMsgHarness(t)
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"send", "chat-1", "no mention here"}, nil, &stdout, &stderr, h.env())
	if code == 0 {
		t.Fatalf("expected nonzero exit when message has no @-mention")
	}
}

func TestSend_ErrorsWhenHandleNotInPeers(t *testing.T) {
	h := newMsgHarness(t)
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"send", "chat-1", "@alice/ghost hello"}, nil, &stdout, &stderr, h.env())
	if code == 0 {
		t.Fatalf("expected nonzero exit when handle is not in peers")
	}
	if !strings.Contains(stderr.String(), "ghost") {
		t.Errorf("expected error to name the missing handle, got: %s", stderr.String())
	}
}

func TestInbox_ListsNotifications(t *testing.T) {
	h := newMsgHarness(t)
	h.writeInbox(t, []inbox.Notification{
		{Band: &inbox.BandFields{
			ChatID:     "chat-1",
			MessageID:  "msg-1",
			SenderID:   "peer-bob",
			SenderName: "bob",
			SenderType: "Agent",
			Content:    "ping",
		}},
	})
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"inbox"}, nil, &stdout, &stderr, h.env())
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stderr.String())
	}
	for _, want := range []string{"msg-1", "bob", "Agent", "ping"} {
		if !strings.Contains(stdout.String(), want) {
			t.Errorf("inbox output missing %q\ngot:\n%s", want, stdout.String())
		}
	}
}

func TestInbox_EmptyShowsHint(t *testing.T) {
	h := newMsgHarness(t)
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"inbox"}, nil, &stdout, &stderr, h.env())
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "empty") {
		t.Errorf("expected 'empty' hint, got: %s", stdout.String())
	}
}

func TestAck_MarksProcessed(t *testing.T) {
	h := newMsgHarness(t)
	h.writeInbox(t, []inbox.Notification{
		{Band: &inbox.BandFields{ChatID: "chat-1", MessageID: "msg-1", SenderID: "peer-bob", SenderName: "bob"}},
	})
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"ack", "msg-1"}, nil, &stdout, &stderr, h.env())
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stderr.String())
	}
	if h.processedHits.Load() != 1 {
		t.Errorf("expected 1 processed call, got %d", h.processedHits.Load())
	}
}

func TestAck_ErrorsWhenMsgNotInInbox(t *testing.T) {
	h := newMsgHarness(t)
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"ack", "nope"}, nil, &stdout, &stderr, h.env())
	if code == 0 {
		t.Fatalf("expected nonzero exit when message id not in inbox")
	}
}

func TestReply_SendsAndAcks(t *testing.T) {
	h := newMsgHarness(t)
	h.writeInbox(t, []inbox.Notification{
		{Band: &inbox.BandFields{
			ChatID:     "chat-1",
			MessageID:  "msg-1",
			SenderID:   "peer-bob",
			SenderName: "bob",
		}},
	})
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"reply", "msg-1", "got it"}, nil, &stdout, &stderr, h.env())
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stderr.String())
	}

	body := h.sentMessageBody(t)
	msg := body["message"].(map[string]any)
	content := msg["content"].(string)
	if !strings.Contains(content, "@bob") || !strings.Contains(content, "got it") {
		t.Errorf("reply content malformed: %s", content)
	}
	mentions := msg["mentions"].([]any)
	if len(mentions) != 1 {
		t.Fatalf("expected 1 mention, got %v", mentions)
	}
	m := mentions[0].(map[string]any)
	if m["id"] != "peer-bob" || m["name"] != "bob" {
		t.Errorf("auto-mention wrong: %v", m)
	}
	if h.processedHits.Load() != 1 {
		t.Errorf("expected reply to auto-ack; got %d processed calls", h.processedHits.Load())
	}
}
