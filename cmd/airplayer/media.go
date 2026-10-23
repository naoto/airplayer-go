package main

import (
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"regexp"
	"strings"
)

// supportedTypes maps playable file extensions to the MIME type served to
// the device.
//
// https://developer.apple.com/library/archive/documentation/AudioVideo/Conceptual/AirPlayGuide/PreparingYourMediaforAirPlay/PreparingYourMediaforAirPlay.html
var supportedTypes = map[string]string{
	".ts":  "video/MP2T",
	".mov": "video/quicktime",
	".m4v": "video/mp4",
	".mp4": "video/mp4",
}

var youtubeHost = regexp.MustCompile(`youtube|youtu\.be`)

// MediaType tells whether a Media is a local file or an online video.
type MediaType int

const (
	FileMedia MediaType = iota
	URLMedia
)

// Media is a video to play.
type Media struct {
	Title string
	Path  string
	Type  MediaType
}

// NewMedia returns the Media for target, a local file path or a URL.
// YouTube URLs are resolved with youtube-dl.
func NewMedia(target string) (*Media, error) {
	if p, err := expandPath(target); err == nil {
		if fi, err := os.Stat(p); err == nil && !fi.IsDir() {
			return &Media{Title: filepath.Base(p), Path: p, Type: FileMedia}, nil
		}
	}

	u, err := url.Parse(target)
	if err != nil {
		return nil, err
	}
	if youtubeHost.MatchString(u.Host) {
		return youtubeMedia(target)
	}

	title := path.Base(u.Path)
	if title == "/" || title == "." {
		title = target
	}
	return &Media{Title: title, Path: u.String(), Type: URLMedia}, nil
}

// Playable reports whether target looks like a video AirPlay can play.
func Playable(target string) bool {
	p := target
	if u, err := url.Parse(target); err == nil && u.Scheme != "" {
		if youtubeHost.MatchString(u.Host) {
			return true
		}
		p = u.Path
	}
	_, ok := supportedTypes[strings.ToLower(filepath.Ext(p))]
	return ok
}

// ContentType returns the MIME type for a playable file name.
func ContentType(name string) string {
	return supportedTypes[strings.ToLower(filepath.Ext(name))]
}

func youtubeMedia(target string) (*Media, error) {
	streamURL, err := youtubeDL("-g", target)
	if err != nil {
		return nil, err
	}
	title, err := youtubeDL("-e", target)
	if err != nil {
		return nil, err
	}
	return &Media{Title: title, Path: streamURL, Type: URLMedia}, nil
}

// youtubeDL runs youtube-dl (or yt-dlp) and returns the first line of output.
func youtubeDL(flag, target string) (string, error) {
	bin, err := exec.LookPath("youtube-dl")
	if err != nil {
		if bin, err = exec.LookPath("yt-dlp"); err != nil {
			return "", fmt.Errorf("youtube-dl or yt-dlp is required to play YouTube videos")
		}
	}
	out, err := exec.Command(bin, flag, target).Output()
	if err != nil {
		return "", fmt.Errorf("%s %s %s: %v", filepath.Base(bin), flag, target, err)
	}
	return strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0]), nil
}

// expandPath expands a leading "~" and returns an absolute path.
func expandPath(p string) (string, error) {
	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", err
		}
		p = filepath.Join(home, p[1:])
	}
	return filepath.Abs(p)
}
