package secretstore

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
)

type disk struct {
	Items map[string]string `json:"items"`
}

type Store struct {
	path string
	mu   sync.Mutex
}

func New(path string) *Store { return &Store{path: path} }

func (s *Store) Put(ref, value string) error {
	if ref == "" {
		return errors.New("empty credential ref")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.load()
	if err != nil {
		return err
	}
	enc, err := protect([]byte(value))
	if err != nil {
		return err
	}
	d.Items[ref] = base64.StdEncoding.EncodeToString(enc)
	return s.save(d)
}

func (s *Store) Get(ref string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.load()
	if err != nil {
		return "", err
	}
	raw, ok := d.Items[ref]
	if !ok {
		return "", os.ErrNotExist
	}
	enc, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", err
	}
	dec, err := unprotect(enc)
	if err != nil {
		return "", err
	}
	return string(dec), nil
}

func (s *Store) Delete(ref string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	d, err := s.load()
	if err != nil {
		return err
	}
	delete(d.Items, ref)
	return s.save(d)
}

func (s *Store) load() (disk, error) {
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		return disk{Items: map[string]string{}}, nil
	}
	if err != nil {
		return disk{}, err
	}
	var d disk
	if err := json.Unmarshal(b, &d); err != nil {
		return disk{}, err
	}
	if d.Items == nil {
		d.Items = map[string]string{}
	}
	return d, nil
}

func (s *Store) save(d disk) error {
	if err := os.MkdirAll(filepath.Dir(s.path), 0700); err != nil {
		return err
	}
	b, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}
