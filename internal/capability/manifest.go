// Package capability defines the verified typed tool manifest.
package capability

import (
	"errors"
	"sort"
	"sync"
)

type Status string

const (
	Available      Status = "available"
	Gated          Status = "gated"
	Unsupported    Status = "unsupported"
	ServerRequired Status = "server_required"
	NotValidated   Status = "not_validated"
)

type Entry struct {
	Method, Scope string
	Status        Status
	Reason        string
}

type Manifest struct {
	mu      sync.RWMutex
	entries map[string]Entry
}

func New(entries []Entry) (*Manifest, error) {
	m := &Manifest{entries: make(map[string]Entry, len(entries))}
	for _, entry := range entries {
		if entry.Method == "" || entry.Scope == "" || entry.Status == "" {
			return nil, errors.New("capability method, scope, and status are required")
		}
		if _, exists := m.entries[entry.Method]; exists {
			return nil, errors.New("duplicate capability method")
		}
		m.entries[entry.Method] = entry
	}
	return m, nil
}
func (m *Manifest) Allows(method, scope string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.entries[method]
	return ok && entry.Status == Available && entry.Scope == scope
}
func (m *Manifest) Entry(method string) (Entry, bool) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entry, ok := m.entries[method]
	return entry, ok
}

// Entries returns a stable snapshot for diagnostics and capability discovery.
func (m *Manifest) Entries() []Entry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	entries := make([]Entry, 0, len(m.entries))
	for _, entry := range m.entries {
		entries = append(entries, entry)
	}
	sort.Slice(entries, func(i, j int) bool { return entries[i].Method < entries[j].Method })
	return entries
}
