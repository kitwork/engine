package insights

import (
	"sync"

	"github.com/kitwork/engine/capabilities"
)

type Metric struct {
	Query string
	Hits  int64
}

type Manager struct {
	mu      sync.Mutex
	scope   capabilities.Scope
	metrics map[string]*Metric
}

func NewManager(scope capabilities.Scope) *Manager {
	return &Manager{
		scope:   scope,
		metrics: make(map[string]*Metric),
	}
}

func (m *Manager) Track(query string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	item, exists := m.metrics[query]
	if !exists {
		item = &Metric{Query: query, Hits: 0}
		m.metrics[query] = item
	}
	item.Hits++
}

func (m *Manager) TopQueries() []*Metric {
	m.mu.Lock()
	defer m.mu.Unlock()

	list := make([]*Metric, 0, len(m.metrics))
	for _, item := range m.metrics {
		list = append(list, item)
	}
	return list
}
