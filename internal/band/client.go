package band

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"
)

type Client struct {
	BaseURL    string
	APIKey     string
	HTTPClient *http.Client
}

func New(baseURL, apiKey string) *Client {
	return &Client{
		BaseURL:    baseURL,
		APIKey:     apiKey,
		HTTPClient: &http.Client{Timeout: 15 * time.Second},
	}
}

type Profile struct {
	ID                string `json:"id"`
	Email             string `json:"email"`
	FirstName         string `json:"first_name"`
	LastName          string `json:"last_name"`
	Role              string `json:"role"`
	ListedInDirectory bool   `json:"listed_in_directory"`
}

func (c *Client) GetProfile() (*Profile, error) {
	var env struct {
		Data Profile `json:"data"`
	}
	if err := c.do("GET", "/api/v1/me/profile", nil, &env); err != nil {
		return nil, err
	}
	return &env.Data, nil
}

type Agent struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Description string `json:"description"`
	IsExternal  bool   `json:"is_external"`
}

func (c *Client) ListAgents() ([]Agent, error) {
	var env struct {
		Data []Agent `json:"data"`
	}
	if err := c.do("GET", "/api/v1/me/agents", nil, &env); err != nil {
		return nil, err
	}
	return env.Data, nil
}

type RegisteredAgent struct {
	Agent  Agent  `json:"agent"`
	APIKey string `json:"-"`
}

// RegisterAgent provisions a new external agent. Name must be 3-100 chars;
// description must be 10-500 chars. The returned APIKey is shown only once.
func (c *Client) RegisterAgent(name, description string) (*RegisteredAgent, error) {
	body, err := json.Marshal(map[string]any{
		"agent": map[string]string{"name": name, "description": description},
	})
	if err != nil {
		return nil, err
	}
	var env struct {
		Data struct {
			Agent       Agent `json:"agent"`
			Credentials struct {
				APIKey string `json:"api_key"`
			} `json:"credentials"`
		} `json:"data"`
	}
	if err := c.do("POST", "/api/v1/me/agents/register", bytes.NewReader(body), &env); err != nil {
		return nil, err
	}
	return &RegisteredAgent{Agent: env.Data.Agent, APIKey: env.Data.Credentials.APIKey}, nil
}

// DeleteAgent removes a user-owned agent. force=true tells Band to delete
// agents that have execution history (otherwise 422). Ephemeral session
// agents managed by `jam daemon` always need force=true since they will
// have sent or received messages by the time stop runs.
func (c *Client) DeleteAgent(id string, force bool) error {
	path := "/api/v1/me/agents/" + id
	if force {
		path += "?force=true"
	}
	return c.do("DELETE", path, nil, nil)
}

type Identity struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Handle string `json:"handle"`
}

type Peer struct {
	ID     string `json:"id"`
	Name   string `json:"name"`
	Handle string `json:"handle"`
	Type   string `json:"type"`
}

type Mention struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// ListPeers returns the agent's peer network (other agents and users it can
// recruit/message). The client must be constructed with an *agent* API key.
func (c *Client) ListPeers() ([]Peer, error) {
	var env struct {
		Data []Peer `json:"data"`
	}
	if err := c.do("GET", "/api/v1/agent/peers?page_size=100", nil, &env); err != nil {
		return nil, err
	}
	return env.Data, nil
}

// SendChatMessage posts a text message into a chat. Band requires at least
// one resolved @-mention in the mentions array. Returns the new message ID.
func (c *Client) SendChatMessage(chatID, content string, mentions []Mention) (string, error) {
	body, err := json.Marshal(map[string]any{
		"message": map[string]any{
			"content":  content,
			"mentions": mentions,
		},
	})
	if err != nil {
		return "", err
	}
	var env struct {
		Data struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := c.do("POST", "/api/v1/agent/chats/"+chatID+"/messages", bytes.NewReader(body), &env); err != nil {
		return "", err
	}
	return env.Data.ID, nil
}

// MarkProcessed marks an inbound message as processed. Must be called for every
// inbound (even ones not replied to) or Band stalls the per-(agent,chat) cursor.
func (c *Client) MarkProcessed(chatID, msgID string) error {
	return c.do("POST", "/api/v1/agent/chats/"+chatID+"/messages/"+msgID+"/processed", bytes.NewReader([]byte("{}")), nil)
}

// AgentMe queries /api/v1/agent/me using the supplied agent API key (not the
// user key on this client). Used to discover the handle of a freshly-registered
// agent without needing a second client.
func (c *Client) AgentMe(agentKey string) (*Identity, error) {
	req, err := http.NewRequest("GET", c.BaseURL+"/api/v1/agent/me", nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("x-api-key", agentKey)
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return nil, &APIError{Status: resp.StatusCode, Body: string(b)}
	}
	var env struct {
		Data Identity `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&env); err != nil {
		return nil, err
	}
	return &env.Data, nil
}

// do is the shared request helper. body=nil for GET; out=nil to skip decoding.
func (c *Client) do(method, path string, body io.Reader, out any) error {
	req, err := http.NewRequest(method, c.BaseURL+path, body)
	if err != nil {
		return err
	}
	req.Header.Set("x-api-key", c.APIKey)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.HTTPClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 400 {
		b, _ := io.ReadAll(resp.Body)
		return &APIError{Status: resp.StatusCode, Body: string(b)}
	}
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

type APIError struct {
	Status int
	Body   string
}

func (e *APIError) Error() string {
	return fmt.Sprintf("band api: HTTP %d: %s", e.Status, e.Body)
}
