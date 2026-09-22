package registry

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
)

var ErrNotFound = errors.New("subject not found")
var ErrConflict = errors.New("revision conflict")

type Version struct {
	Subject     string          `json:"subject"`
	Number      int             `json:"number"`
	Fingerprint string          `json:"fingerprint"`
	Schema      json.RawMessage `json:"schema"`
}

type Store struct {
	mu       sync.RWMutex
	tenants  map[string]map[string][]Version
	revision uint64
}

func NewStore() *Store { return &Store{tenants: make(map[string]map[string][]Version)} }

func CanonicalJSON(raw []byte) ([]byte, error) {
	var value any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&value); err != nil {
		return nil, fmt.Errorf("invalid JSON: %w", err)
	}
	if dec.More() {
		return nil, errors.New("trailing JSON")
	}
	normalized := normalize(value)
	return json.Marshal(normalized)
}

func normalize(v any) any {
	switch x := v.(type) {
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		out := make(map[string]any, len(x))
		for _, k := range keys {
			out[k] = normalize(x[k])
		}
		return out
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = normalize(x[i])
		}
		return out
	default:
		return v
	}
}

func (s *Store) Register(tenant, subject string, raw []byte, expected *uint64) (Version, error) {
	canonical, err := CanonicalJSON(raw)
	if err != nil {
		return Version{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if expected != nil && *expected != s.revision {
		return Version{}, ErrConflict
	}
	subjects := s.tenants[tenant]
	if subjects == nil {
		subjects = make(map[string][]Version)
		s.tenants[tenant] = subjects
	}
	versions := subjects[subject]
	v := Version{Subject: subject, Number: len(versions) + 1, Schema: append([]byte(nil), canonical...)}
	sum := sha256.Sum256(canonical)
	v.Fingerprint = hex.EncodeToString(sum[:])
	subjects[subject] = append(versions, v)
	s.revision++
	return v, nil
}

func (s *Store) Latest(tenant, subject string) (Version, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	versions := s.tenants[tenant][subject]
	if len(versions) == 0 {
		return Version{}, ErrNotFound
	}
	v := versions[len(versions)-1]
	v.Schema = append([]byte(nil), v.Schema...)
	return v, nil
}

func (s *Store) Snapshot() ([]byte, uint64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	raw, err := json.Marshal(s.tenants)
	return raw, s.revision, err
}
