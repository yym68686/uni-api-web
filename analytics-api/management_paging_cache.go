package main

import (
	"encoding/json"
	"slices"
	"sync"
)

const managementPagingBytes = 24 << 20
const managementPagingEntries = 12

type managementPagingDirectories struct {
	mu      sync.Mutex
	order   []string
	entries map[string]managementPagingDirectoryEntry
	data    map[string]*managementPagingSharedData
	bytes   int
}

type managementPagingDirectoryEntry struct {
	managementComputedPage
	dataKey string
	reset   bool
}

type managementPagingSharedData struct {
	list        managementListPagingData
	refs, bytes int
}

func (c *managementPagingDirectories) get(key string) (managementComputedPage, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	e, ok := c.entries[key]
	if ok {
		if i := slices.Index(c.order, key); i >= 0 {
			c.order = append(slices.Delete(c.order, i, i+1), key)
		}
	}
	return e.managementComputedPage, ok
}

func (c *managementPagingDirectories) put(key, dataKey string, proof *managementFreshProof, page managementComputedPage, body []byte, reset bool) (stored, shared bool) {
	// Decode outside the lock on a miss; publish the complete detached graph
	// together with its first reference. Get never sees an incomplete entry.
	c.mu.Lock()
	data := c.data[dataKey]
	c.mu.Unlock()
	if data == nil {
		data = &managementPagingSharedData{}
		if err := json.Unmarshal(body, &data.list.data); err != nil {
			return false, false
		}
		data.list.site = make(map[string]subTarget)
		for _, account := range data.list.data.Input.Accounts {
			for _, target := range account.Targets {
				data.list.site[qualityGroupID(account.ID, target.GroupID)] = target
			}
		}
		data.list.headers = managementAccountHeaders(data.list.data.Input.Accounts)
		data.bytes = 2*len(body) + 768*len(data.list.site)
	}
	if page.Bytes+data.bytes > managementPagingBytes {
		return false, false
	}

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.entries == nil {
		c.entries = make(map[string]managementPagingDirectoryEntry)
		c.data = make(map[string]*managementPagingSharedData)
	}
	if current := c.data[dataKey]; current != nil {
		data, shared = current, true
	} else {
		// The last reference may have been evicted while we were unlocked.
		data.refs = 0
		c.data[dataKey] = data
		c.bytes += data.bytes
	}
	data.refs++
	if _, exists := c.entries[key]; exists {
		c.remove(slices.Index(c.order, key))
	}
	for len(c.order) >= managementPagingEntries || c.bytes+page.Bytes > managementPagingBytes {
		i := slices.IndexFunc(c.order, func(k string) bool { return c.entries[k].reset })
		if i < 0 {
			if reset {
				// Maintenance may not evict interactive filter directories.
				c.release(dataKey)
				return false, false
			}
			i = 0
		}
		c.remove(i)
	}
	page.ListPlan.data = &data.list
	page.Proof = managementRetainedProof(*proof)
	c.entries[key] = managementPagingDirectoryEntry{page, dataKey, reset}
	c.order = append(c.order, key)
	c.bytes += page.Bytes
	return true, shared
}

func (c *managementPagingDirectories) release(key string) {
	data := c.data[key]
	data.refs--
	if data.refs == 0 {
		delete(c.data, key)
		c.bytes -= data.bytes
	}
}

func (c *managementPagingDirectories) remove(i int) {
	key := c.order[i]
	entry := c.entries[key]
	c.order = slices.Delete(c.order, i, i+1)
	delete(c.entries, key)
	c.bytes -= entry.Bytes
	c.release(entry.dataKey)
}
