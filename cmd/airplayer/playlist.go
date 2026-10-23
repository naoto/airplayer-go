package main

import (
	"errors"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
)

// Playlist is an ordered list of media.
type Playlist struct {
	Shuffle bool
	Repeat  bool
	items   []*Media
}

// Add adds target to the playlist. A directory adds every playable file in
// it, sorted by name.
func (p *Playlist) Add(target string) error {
	if dir, err := expandPath(target); err == nil {
		if fi, err := os.Stat(dir); err == nil && fi.IsDir() {
			return p.addDir(dir)
		}
	}
	if !Playable(target) {
		return fmt.Errorf("unsupported media: %s", target)
	}
	m, err := NewMedia(target)
	if err != nil {
		return err
	}
	p.items = append(p.items, m)
	return nil
}

func (p *Playlist) addDir(dir string) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	for _, e := range entries {
		if e.IsDir() || !Playable(e.Name()) {
			continue
		}
		m, err := NewMedia(filepath.Join(dir, e.Name()))
		if err != nil {
			return err
		}
		p.items = append(p.items, m)
	}
	return nil
}

// Len returns the number of media in the playlist.
func (p *Playlist) Len() int {
	return len(p.items)
}

// Each calls fn for every media, shuffling before each round if Shuffle is
// set and looping forever if Repeat is set. It stops at the first error.
func (p *Playlist) Each(fn func(*Media) error) error {
	if len(p.items) == 0 {
		return errors.New("no playable media")
	}
	for {
		if p.Shuffle {
			rand.Shuffle(len(p.items), func(i, j int) {
				p.items[i], p.items[j] = p.items[j], p.items[i]
			})
		}
		for _, m := range p.items {
			if err := fn(m); err != nil {
				return err
			}
		}
		if !p.Repeat {
			return nil
		}
	}
}
