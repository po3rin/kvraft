package store

import (
	"bytes"
	"context"
	"encoding/gob"
	"errors"
	"sync"
	"time"
)

type Raft interface {
	Propose(prop []byte) error
	Commit() <-chan string
	DoneReplayWAL() <-chan struct{}
}

type kv struct {
	Key string
	Val string
}

type Store struct {
	mu      sync.RWMutex
	kvStore map[string]string
	Raft
}

func New(raft Raft) *Store {
	return &Store{
		kvStore: make(map[string]string),
		Raft:    raft,
	}
}

func (s *Store) Lookup(key string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.kvStore[key]
	return v, ok
}

func (s *Store) Save(key string, value string) error {
	var buf bytes.Buffer
	err := gob.NewEncoder(&buf).Encode(kv{key, value})
	if err != nil {
		return err
	}

	err = s.Propose(buf.Bytes())
	if err != nil {
		return err
	}
	return nil
}

func (s *Store) RunCommitReader(ctx context.Context) error {
	// 起動時にWALのreplayを待つ
	select {
	case <-s.Raft.DoneReplayWAL():
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(10 * time.Second):
		return errors.New(
			"timeout(10s) receiving done replay channel",
		)
	}

	for {
		select {
		// コミット済みエントリ（適用していいエントリ）を受けて
		// map[string]stringにappendする
		case data := <-s.Raft.Commit():
			var kvdata kv
			dec := gob.NewDecoder(bytes.NewBufferString(data))
			if err := dec.Decode(&kvdata); err != nil {
				return err
			}

			s.mu.Lock()
			s.kvStore[kvdata.Key] = kvdata.Val
			s.mu.Unlock()

		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
