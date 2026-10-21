package main

import (
	"container/list"
	"sync"
	"time"
)

// A small in-process LRU cache with per-entry TTL.
//
// This sits in front of Redis as an L1 cache: the hottest short codes get
// served straight from app memory, skipping the network round-trip to Redis
// entirely. Real-world URL shortener traffic is heavily power-law distributed
// (a small fraction of links get most of the clicks), so this is where a lot
// of the cheap wins for a redirect-heavy workload actually live.
//
// Built by hand (a map plus container/list) rather than pulling in a library
// since the point of this project is understanding the mechanism, not just
// having it.
type LRUCache struct {
	maxSize int
	ttl     time.Duration
	mu      sync.Mutex
	store   *list.List // front = most recently used, back = least recently used
	items   map[string]*list.Element
}

type lruEntry struct {
	key       string
	value     string
	expiresAt time.Time
}

func NewLRUCache(maxSize int, ttl time.Duration) *LRUCache {
	return &LRUCache{
		maxSize: maxSize,
		ttl:     ttl,
		store:   list.New(),
		items:   make(map[string]*list.Element, maxSize),
	}
}

func (c *LRUCache) Get(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()

	element, ok := c.items[key]
	if !ok {
		return "", false
	}

	entry := element.Value.(*lruEntry)
	if entry.expiresAt.Before(time.Now()) {
		// expired -> treat as a miss and evict it
		c.store.Remove(element)
		delete(c.items, key)
		return "", false
	}

	// move to the front = "most recently used"
	c.store.MoveToFront(element)
	return entry.value, true
}

func (c *LRUCache) Set(key, value string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if element, ok := c.items[key]; ok {
		entry := element.Value.(*lruEntry)
		entry.value = value
		entry.expiresAt = time.Now().Add(c.ttl)
		c.store.MoveToFront(element)
		return
	}

	element := c.store.PushFront(&lruEntry{key: key, value: value, expiresAt: time.Now().Add(c.ttl)})
	c.items[key] = element

	if c.store.Len() > c.maxSize {
		// evict the least recently used entry (the back of the list)
		oldest := c.store.Back()
		c.store.Remove(oldest)
		delete(c.items, oldest.Value.(*lruEntry).key)
	}
}

func (c *LRUCache) Len() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.store.Len()
}
