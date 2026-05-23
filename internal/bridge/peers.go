package bridge

import (
	"sync"

	"github.com/ed-lepedus-thenvoi/jam/internal/band"
)

// peerCache holds the agent's view of other agents/users it can reach. Used
// for two things: resolving sender_id → full handle on inbound, and rewriting
// `@[[uuid]]` patterns in inbound message text to `@owner/handle` so what the
// model sees matches what `jam send` will accept on the way back out.
//
// The cache lazily refreshes from /agent/peers on miss. A synthetic self
// entry covers the agent's own UUID (you're not in your own peers list, but
// the platform happily mentions you as @[[<self>]] when others address you).
type peerCache struct {
	mu     sync.RWMutex
	self   band.Peer
	byID   map[string]band.Peer
	client *band.Client
}

func newPeerCache(client *band.Client, identity *band.Identity) *peerCache {
	return &peerCache{
		self: band.Peer{
			ID:     identity.ID,
			Name:   identity.Name,
			Handle: identity.Handle,
			Type:   "Agent",
		},
		byID:   map[string]band.Peer{},
		client: client,
	}
}

// lookupByID returns the peer for the given UUID. On miss, refreshes once
// from /agent/peers and retries. Self resolves without an API call.
func (c *peerCache) lookupByID(id string) (band.Peer, bool) {
	if id == c.self.ID {
		return c.self, true
	}
	c.mu.RLock()
	p, ok := c.byID[id]
	c.mu.RUnlock()
	if ok {
		return p, true
	}
	if err := c.refresh(); err != nil {
		return band.Peer{}, false
	}
	c.mu.RLock()
	defer c.mu.RUnlock()
	p, ok = c.byID[id]
	return p, ok
}

func (c *peerCache) refresh() error {
	peers, err := c.client.ListPeers()
	if err != nil {
		return err
	}
	c.mu.Lock()
	c.byID = make(map[string]band.Peer, len(peers))
	for _, p := range peers {
		c.byID[p.ID] = p
	}
	c.mu.Unlock()
	return nil
}
