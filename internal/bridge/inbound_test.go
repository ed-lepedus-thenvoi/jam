package bridge

import (
	"bytes"
	"encoding/json"
	"log"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/ed-lepedus-thenvoi/jam/internal/band"
	"github.com/ed-lepedus-thenvoi/jam/internal/inbox"
)

func TestHandleIncoming_PostsProcessingAndWritesInbox(t *testing.T) {
	home := t.TempDir()
	var processedHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/agent/chats/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/processing") {
			processedHits.Add(1)
			w.WriteHeader(200)
			_, _ = w.Write([]byte(`{"data":{"id":"x","status":"processing","success":true}}`))
			return
		}
		w.WriteHeader(404)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := Config{
		BaseURL:      srv.URL,
		AgentAPIKey:  "band_a_test",
		HomeDir:      home,
		TeamName:     "test-team",
		TeammateName: "team-lead",
	}
	tmpl, err := loadTemplate("")
	if err != nil {
		t.Fatal(err)
	}
	client := band.New(cfg.BaseURL, cfg.AgentAPIKey)
	logger := log.New(&bytes.Buffer{}, "", 0)

	msg := incomingMessage{
		ChatID:     "chat-1",
		MessageID:  "msg-1",
		SenderName: "bob",
		SenderID:   "peer-bob",
		SenderType: "Agent",
		Content:    "hello there",
	}
	handleIncoming(cfg, tmpl, client, logger, msg)

	if got := processedHits.Load(); got != 1 {
		t.Errorf("expected 1 processing call, got %d", got)
	}

	data, err := os.ReadFile(inbox.Path(home, "test-team", "team-lead"))
	if err != nil {
		t.Fatalf("inbox file not written: %v", err)
	}
	var notes []inbox.Notification
	if err := json.Unmarshal(data, &notes); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, data)
	}
	if len(notes) != 1 {
		t.Fatalf("want 1 notification, got %d", len(notes))
	}
	n := notes[0]
	if n.From != "thenvoi-platform" {
		t.Errorf("From = %q", n.From)
	}
	if n.Band == nil || n.Band.ChatID != "chat-1" || n.Band.MessageID != "msg-1" {
		t.Errorf("band fields wrong: %+v", n.Band)
	}
	for _, want := range []string{"bob", "Agent", "msg-1", "jam reply msg-1", "jam ack msg-1"} {
		if !strings.Contains(n.Text, want) {
			t.Errorf("rendered text missing %q\ngot:\n%s", want, n.Text)
		}
	}
}

func TestHandleIncoming_NoTeamSkipsInbox(t *testing.T) {
	home := t.TempDir()
	var processedHits atomic.Int32
	mux := http.NewServeMux()
	mux.HandleFunc("/api/v1/agent/chats/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/processing") {
			processedHits.Add(1)
			w.WriteHeader(200)
		}
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cfg := Config{
		BaseURL:     srv.URL,
		AgentAPIKey: "band_a_test",
		HomeDir:     home,
		// TeamName + TeammateName intentionally empty
	}
	tmpl, _ := loadTemplate("")
	client := band.New(cfg.BaseURL, cfg.AgentAPIKey)
	logger := log.New(&bytes.Buffer{}, "", 0)

	handleIncoming(cfg, tmpl, client, logger, incomingMessage{
		ChatID: "c", MessageID: "m", SenderName: "x",
	})

	if processedHits.Load() != 1 {
		t.Errorf("expected mark_processing to fire regardless of inbox config")
	}
	// No inbox file should exist
	entries, _ := os.ReadDir(home)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ".claude") {
			t.Errorf("inbox dir should not be created when no team configured")
		}
	}
}

func TestHandleIncoming_AppendsMultipleNotifications(t *testing.T) {
	home := t.TempDir()
	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(200) })
	srv := httptest.NewServer(mux)
	defer srv.Close()
	cfg := Config{BaseURL: srv.URL, AgentAPIKey: "k", HomeDir: home, TeamName: "t", TeammateName: "tl"}
	tmpl, _ := loadTemplate("")
	client := band.New(cfg.BaseURL, cfg.AgentAPIKey)
	logger := log.New(&bytes.Buffer{}, "", 0)

	for i, mid := range []string{"m1", "m2", "m3"} {
		handleIncoming(cfg, tmpl, client, logger, incomingMessage{
			ChatID: "c", MessageID: mid, SenderName: "x",
			SenderID: "id", SenderType: "Agent", Content: "n",
		})
		_ = i
	}
	data, _ := os.ReadFile(inbox.Path(home, "t", "tl"))
	var notes []inbox.Notification
	_ = json.Unmarshal(data, &notes)
	if len(notes) != 3 {
		t.Errorf("expected 3 appended notifications, got %d", len(notes))
	}
}
