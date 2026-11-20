package config

import "errors"

// SetProviderEnabled turns a provider on or off without touching its credentials.
func (s *Store) SetProviderEnabled(id string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.cfg.Providers {
		if s.cfg.Providers[i].ID == id {
			s.cfg.Providers[i].Enabled = enabled
			return s.saveLocked()
		}
	}
	return errors.New("provider not found")
}

// SetAccountEnabled turns an account connector on or off without logging it out.
func (s *Store) SetAccountEnabled(id string, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.cfg.Accounts {
		if s.cfg.Accounts[i].ID == id {
			s.cfg.Accounts[i].Enabled = enabled
			return s.saveLocked()
		}
	}
	return errors.New("account connector not found")
}
