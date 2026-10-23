package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPlayable(t *testing.T) {
	tests := map[string]bool{
		"/Movies/AKIRA.m4v":                          true,
		"007 SKYFALL.MP4":                            true,
		"clip.mov":                                   true,
		"stream.ts":                                  true,
		"http://heinlein.local/Movies/AKIRA.m4v":     true,
		"http://example.com/a.mp4?token=1":           true,
		"http://www.youtube.com/watch?v=QH2-TGUlwu4": true,
		"http://youtu.be/QH2-TGUlwu4":                true,
		"movie.avi":                                  false,
		"http://example.com/rss.xml":                 false,
	}
	for target, want := range tests {
		if got := Playable(target); got != want {
			t.Errorf("Playable(%q) = %v, want %v", target, got, want)
		}
	}
}

func TestNewMediaFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "007 SKYFALL.mp4")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	m, err := NewMedia(path)
	if err != nil {
		t.Fatal(err)
	}
	if m.Type != FileMedia || m.Path != path || m.Title != "007 SKYFALL.mp4" {
		t.Errorf("NewMedia() = %+v", m)
	}
}

func TestNewMediaURL(t *testing.T) {
	m, err := NewMedia("http://heinlein.local/Movies/GHOST IN THE SHELL.m4v")
	if err != nil {
		t.Fatal(err)
	}
	if m.Type != URLMedia || m.Title != "GHOST IN THE SHELL.m4v" ||
		m.Path != "http://heinlein.local/Movies/GHOST%20IN%20THE%20SHELL.m4v" {
		t.Errorf("NewMedia() = %+v", m)
	}
}

func TestExpandPath(t *testing.T) {
	home, _ := os.UserHomeDir()
	got, err := expandPath("~/Movies")
	if err != nil || got != filepath.Join(home, "Movies") {
		t.Errorf("expandPath() = %q, %v", got, err)
	}
}

func TestPlaylistDir(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"b.mp4", "a.m4v", "notes.txt", "c.mov"} {
		os.WriteFile(filepath.Join(dir, name), nil, 0o644)
	}
	os.Mkdir(filepath.Join(dir, "sub.mp4"), 0o755)

	p := &Playlist{}
	if err := p.Add(dir); err != nil {
		t.Fatal(err)
	}
	var titles []string
	p.Each(func(m *Media) error {
		titles = append(titles, m.Title)
		return nil
	})
	if got := strings.Join(titles, ","); got != "a.m4v,b.mp4,c.mov" {
		t.Errorf("titles = %s", got)
	}
}

func TestPlaylistUnsupported(t *testing.T) {
	p := &Playlist{}
	if err := p.Add("http://example.com/rss.xml"); err == nil {
		t.Error("expected error")
	}
	if err := p.Each(func(*Media) error { return nil }); err == nil {
		t.Error("expected error for empty playlist")
	}
}

func TestPlaylistShuffle(t *testing.T) {
	p := &Playlist{Shuffle: true}
	for _, u := range []string{"http://x/1.mp4", "http://x/2.mp4", "http://x/3.mp4"} {
		if err := p.Add(u); err != nil {
			t.Fatal(err)
		}
	}
	n := 0
	p.Each(func(*Media) error { n++; return nil })
	if n != 3 {
		t.Errorf("played %d, want 3", n)
	}
}

func TestFormatDuration(t *testing.T) {
	if got := formatDuration(time.Hour + 4*time.Minute + 25*time.Second); got != "01:04:25" {
		t.Errorf("formatDuration() = %s", got)
	}
}
