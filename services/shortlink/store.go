package main

import (
	"crypto/rand"
	"errors"
	"fmt"
	"sync"
)

const (
	// codeLength is how many characters a short code has.
	codeLength = 7
	// codeAlphabet leaves out characters that are easy to misread: 0, O, 1, l and I.
	codeAlphabet = "abcdefghijkmnopqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	// codeAttempts is how many random codes add tries before it gives up on finding an unused one.
	codeAttempts = 5
)

// store keeps links in memory. It is safe for concurrent use.
type store struct {
	mu    sync.RWMutex
	links map[string]string
}

func newStore() *store {
	return &store{links: map[string]string{}}
}

// add stores target under a new random code and returns the code.
func (s *store) add(target string) (string, error) {
	for range codeAttempts {
		code, err := randomCode()
		if err != nil {
			return "", err
		}
		s.mu.Lock()
		if _, taken := s.links[code]; !taken {
			s.links[code] = target
			s.mu.Unlock()
			return code, nil
		}
		s.mu.Unlock()
	}
	return "", errors.New("no unused code found")
}

// get returns the URL stored under code, and whether there is one.
func (s *store) get(code string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	target, found := s.links[code]
	return target, found
}

// randomCode returns codeLength characters from codeAlphabet, chosen with crypto/rand.
func randomCode() (string, error) {
	buf := make([]byte, codeLength)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("reading random bytes: %w", err)
	}
	for i, b := range buf {
		buf[i] = codeAlphabet[int(b)%len(codeAlphabet)]
	}
	return string(buf), nil
}
