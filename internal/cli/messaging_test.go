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

func TestSend_ResolvesHumanAndAgentMentionsTogether(t *testing.T) {
	h := newMsgHarness(t)
	// Peers include both a human and an agent whose handle has the human's
	// handle as a prefix — the exact collision pattern from the v0.1.3 bug.
	h.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agent/peers":
			_, _ = w.Write([]byte(`{"data":[
				{"id":"user-ed","name":"Ed Lepedus","handle":"ed.lepedus","type":"User"},
				{"id":"peer-bob","name":"bob","handle":"ed.lepedus/bob","type":"Agent"}
			]}`))
		default:
			if strings.HasSuffix(r.URL.Path, "/messages") {
				body, _ := io.ReadAll(r.Body)
				h.sendBody.Store(body)
				_, _ = w.Write([]byte(`{"data":{"id":"sent-id"}}`))
			} else {
				w.WriteHeader(404)
			}
		}
	})

	var stdout, stderr bytes.Buffer
	code := Execute([]string{"send", "chat-1", "@ed.lepedus and @ed.lepedus/bob"},
		nil, &stdout, &stderr, h.env())
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stderr.String())
	}
	body := h.sentMessageBody(t)
	mentions := body["message"].(map[string]any)["mentions"].([]any)
	if len(mentions) != 2 {
		t.Fatalf("expected 2 mentions (human + agent), got %d: %v", len(mentions), mentions)
	}
	// Long-handle-first ordering is critical so the platform's
	// String.replace_all doesn't corrupt the agent mention via the
	// human handle prefix.
	if mentions[0].(map[string]any)["handle"] != "ed.lepedus/bob" {
		t.Errorf("first mention must be the longer handle, got %v", mentions[0])
	}
	if mentions[1].(map[string]any)["handle"] != "ed.lepedus" {
		t.Errorf("second mention must be the shorter handle, got %v", mentions[1])
	}
}

func TestSend_LongFirstOrderingHoldsRegardlessOfTextOrder(t *testing.T) {
	h := newMsgHarness(t)
	h.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/api/v1/agent/peers":
			_, _ = w.Write([]byte(`{"data":[
				{"id":"user-ed","name":"Ed Lepedus","handle":"ed.lepedus","type":"User"},
				{"id":"peer-bob","name":"bob","handle":"ed.lepedus/bob","type":"Agent"}
			]}`))
		default:
			if strings.HasSuffix(r.URL.Path, "/messages") {
				body, _ := io.ReadAll(r.Body)
				h.sendBody.Store(body)
				_, _ = w.Write([]byte(`{"data":{"id":"x"}}`))
			}
		}
	})
	// Long-form first in text — sort must still produce the same order.
	if code := Execute([]string{"send", "chat-1", "@ed.lepedus/bob then @ed.lepedus"},
		nil, &bytes.Buffer{}, &bytes.Buffer{}, h.env()); code != 0 {
		t.Fatalf("send failed")
	}
	mentions := h.sentMessageBody(t)["message"].(map[string]any)["mentions"].([]any)
	if mentions[0].(map[string]any)["handle"] != "ed.lepedus/bob" {
		t.Errorf("long handle must come first regardless of text order, got %v", mentions[0])
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

func TestSend_SkipsSenderOwnHandle(t *testing.T) {
	h := newMsgHarness(t)
	// Harness session state has Handle = "alice/self". Mentioning self in
	// content text shouldn't error — Band rejects messages without ANY
	// mention, but the sender's own handle can't be in their peers list
	// (you're never your own peer), so the resolver must skip it.
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"send", "chat-1", "@alice/claude-self @alice/bob ping yourself"}, nil, &stdout, &stderr, h.env())
	if code != 0 {
		t.Fatalf("exit %d\nstderr: %s", code, stderr.String())
	}
	body := h.sentMessageBody(t)
	msg := body["message"].(map[string]any)
	mentions := msg["mentions"].([]any)
	if len(mentions) != 1 {
		t.Fatalf("expected 1 mention (sender skipped), got %d: %v", len(mentions), mentions)
	}
	if mentions[0].(map[string]any)["id"] != "peer-bob" {
		t.Errorf("expected only bob in mentions, got %v", mentions[0])
	}
}

func TestSend_SkipsUnresolvableButProceedsWithRest(t *testing.T) {
	// User's text contains both a real mention AND a prose @-pattern that
	// looks like a handle but doesn't resolve (e.g. `@owner/handle` in docs
	// examples). The send should warn about the bogus one and proceed with
	// the legit one, not abort the whole message.
	h := newMsgHarness(t)
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"send", "chat-1", "see @alice/bob and example @owner/handle"},
		nil, &stdout, &stderr, h.env())
	if code != 0 {
		t.Fatalf("expected send to succeed by skipping the bogus prose-mention; exit %d\n%s", code, stderr.String())
	}
	body := h.sentMessageBody(t)
	mentions := body["message"].(map[string]any)["mentions"].([]any)
	if len(mentions) != 1 {
		t.Errorf("expected only the legit mention to land, got %d: %v", len(mentions), mentions)
	}
	if !strings.Contains(stderr.String(), "owner/handle") {
		t.Errorf("expected stderr warning about owner/handle, got: %s", stderr.String())
	}
}

func TestExtractHandles_RejectsLeadingPunctuation(t *testing.T) {
	// Prose patterns that should NOT be picked up as mentions: `@-mention`
	// (leading hyphen), `@_init` (leading underscore), `@.foo` (leading dot).
	// Real handles always start with alphanumeric.
	cases := map[string][]string{
		"@-mention please":            nil,
		"@_init makes no sense":       nil,
		"@.foo ignored":               nil,
		"@ed.lepedus is fine":         {"ed.lepedus"},
		"@ed.lepedus/claude-foo also": {"ed.lepedus/claude-foo"},
	}
	for in, want := range cases {
		got := extractHandles(in)
		if len(got) != len(want) {
			t.Errorf("extractHandles(%q) = %v; want %v", in, got, want)
			continue
		}
		for i := range want {
			if got[i] != want[i] {
				t.Errorf("extractHandles(%q)[%d] = %q; want %q", in, i, got[i], want[i])
			}
		}
	}
}

func TestSend_ErrorsWhenOnlySenderMentioned(t *testing.T) {
	h := newMsgHarness(t)
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"send", "chat-1", "@alice/claude-self talking to myself"}, nil, &stdout, &stderr, h.env())
	if code == 0 {
		t.Fatalf("expected nonzero exit when only mention is sender's own handle")
	}
}

func TestSend_RetriesResolutionOnMiss(t *testing.T) {
	// Simulates platform peer-list eventual consistency: first /agent/peers
	// call shows an empty list, second call shows the actual peer. Send
	// should retry once and succeed rather than erroring on the first miss.
	home := t.TempDir()
	cwd := t.TempDir()
	var peersCalls atomic.Int32
	var sentBody atomic.Value
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/agent/peers", func(w http.ResponseWriter, r *http.Request) {
		n := peersCalls.Add(1)
		if n == 1 {
			_, _ = w.Write([]byte(`{"data":[]}`))
			return
		}
		_, _ = w.Write([]byte(`{"data":[{"id":"peer-bob","name":"bob","handle":"alice/bob","type":"Agent"}]}`))
	})
	mux.HandleFunc("/api/v1/agent/chats/", func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		sentBody.Store(body)
		_, _ = w.Write([]byte(`{"data":{"id":"sent-msg-id","success":true}}`))
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()
	writeConfig(t, home, srv.URL, "band_u_test")
	scope := session.Scope(cwd)
	if err := session.Save(home, "", &session.State{
		Scope: scope, Cwd: cwd, AgentID: "self", AgentName: "self",
		AgentAPIKey: "band_a_SELF", Handle: "alice/self", PID: 1,
	}); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := Execute([]string{"send", "chat-1", "@alice/bob propagation"}, nil, &stdout, &stderr,
		Env{HomeDir: home, Cwd: cwd, Getenv: func(string) string { return "" }})
	if code != 0 {
		t.Fatalf("expected retry to succeed, got exit %d\n%s", code, stderr.String())
	}
	if got := peersCalls.Load(); got < 2 {
		t.Errorf("expected at least 2 ListPeers calls (initial + retry), got %d", got)
	}
	if sentBody.Load() == nil {
		t.Errorf("expected message to be sent after retry")
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

func TestReply_ResolvesAdditionalMentionsAndKeepsSender(t *testing.T) {
	h := newMsgHarness(t)
	h.writeInbox(t, []inbox.Notification{
		{Band: &inbox.BandFields{
			ChatID: "chat-1", MessageID: "msg-1",
			SenderID: "peer-bob", SenderName: "bob", SenderHandle: "alice/bob",
		}},
	})
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"reply", "msg-1", "@alice/carol heads up — bob's response below"},
		nil, &stdout, &stderr, h.env())
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stderr.String())
	}
	body := h.sentMessageBody(t)
	msg := body["message"].(map[string]any)
	mentions := msg["mentions"].([]any)
	if len(mentions) != 2 {
		t.Fatalf("expected 2 mentions (carol + auto-sender), got %d: %v", len(mentions), mentions)
	}
	ids := map[string]bool{}
	for _, m := range mentions {
		ids[m.(map[string]any)["id"].(string)] = true
	}
	if !ids["peer-bob"] || !ids["peer-carol"] {
		t.Errorf("mention ids = %v; want both peer-bob and peer-carol", ids)
	}
}

func TestReply_DoesNotDoublePrependSenderAlreadyInText(t *testing.T) {
	h := newMsgHarness(t)
	h.writeInbox(t, []inbox.Notification{
		{Band: &inbox.BandFields{
			ChatID: "chat-1", MessageID: "msg-1",
			SenderID: "peer-bob", SenderName: "bob", SenderHandle: "alice/bob",
		}},
	})
	var stdout, stderr bytes.Buffer
	code := Execute([]string{"reply", "msg-1", "@alice/bob explicit mention"},
		nil, &stdout, &stderr, h.env())
	if code != 0 {
		t.Fatalf("exit %d\n%s", code, stderr.String())
	}
	body := h.sentMessageBody(t)
	msg := body["message"].(map[string]any)
	content := msg["content"].(string)
	if strings.Count(content, "@alice/bob") != 1 {
		t.Errorf("expected exactly one @alice/bob in content, got: %s", content)
	}
	mentions := msg["mentions"].([]any)
	if len(mentions) != 1 {
		t.Errorf("expected exactly one mention, got %d", len(mentions))
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
