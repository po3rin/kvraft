package store

import "sync"

type Store struct {
	mu      sync.RWMutex
	kvStore map[string]string
}

func New() *Store {
	return &Store{
		kvStore: make(map[string]string),
	}
}

func (s *Store) Lookup(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.kvStore[key]
	return v, ok
}

func (s *Store) Save(key string, value string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.kvStore[key] = value
	return nil
}
