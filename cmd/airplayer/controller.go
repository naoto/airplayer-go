package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"time"

	"github.com/naoto/airplayer-go/airplay"
)

const (
	pollInterval = time.Second
	startTimeout = time.Minute
)

// Controller plays media on an AirPlay device.
type Controller struct {
	client *airplay.Client
	info   airplay.Info
	out    io.Writer
}

func NewController(client *airplay.Client, out io.Writer) *Controller {
	return &Controller{client: client, info: client.Info(), out: out}
}

func (c *Controller) Close() {
	c.client.Close()
}

// Play plays m and blocks until playback finishes or ctx is cancelled.
func (c *Controller) Play(ctx context.Context, m *Media) error {
	src := m.Path
	if m.Type == FileMedia {
		fs, err := serveFile(m.Path, c.client.LocalIP())
		if err != nil {
			return err
		}
		defer fs.Close()
		src = fs.URL
	}

	fmt.Fprintf(c.out, " Source: %s\n", src)
	fmt.Fprintf(c.out, "  Title: %s\n", m.Title)
	fmt.Fprintf(c.out, " Device: %s (Resolution: %s)\n", c.client.Device.Name, c.info.Resolution)

	if err := c.client.Play(src); err != nil {
		return err
	}
	bar := newProgressBar(c.out)
	err := c.wait(ctx, bar)
	c.client.Stop()
	if err != nil {
		fmt.Fprintln(c.out)
		return err
	}
	bar.Finish()
	return nil
}

// wait polls the playback position until the device stops playing.
func (c *Controller) wait(ctx context.Context, bar *progressBar) error {
	ticker := time.NewTicker(pollInterval)
	defer ticker.Stop()

	begin := time.Now()
	started := false
	failures := 0
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
		}

		info, err := c.client.PlaybackInfo()
		if errors.Is(err, airplay.ErrPairingRequired) {
			return err
		}
		if err != nil {
			if failures++; failures >= 5 {
				return err
			}
			continue
		}
		failures = 0

		switch {
		case info.Duration > 0 && !info.Stopped:
			started = true
			bar.Update("Streaming", info.Position/info.Duration*100)
		case started:
			return nil
		case time.Since(begin) > startTimeout:
			return errors.New("playback did not start")
		}
	}
}

type fileServer struct {
	URL string
	srv *http.Server
}

// serveFile serves the file at path over HTTP on localIP, the address
// the device reaches us at, so the device can stream it.
func serveFile(path string, localIP net.IP) (*fileServer, error) {
	ln, err := net.Listen("tcp", ":7070")
	if err != nil {
		if ln, err = net.Listen("tcp", ":0"); err != nil {
			return nil, err
		}
	}
	port := ln.Addr().(*net.TCPAddr).Port

	name := filepath.Base(path)
	srv := &http.Server{Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f, err := os.Open(path)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		defer f.Close()
		fi, err := f.Stat()
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		w.Header().Set("Content-Type", ContentType(name))
		http.ServeContent(w, r, name, fi.ModTime(), f)
	})}
	go srv.Serve(ln)

	u := url.URL{
		Scheme: "http",
		Host:   net.JoinHostPort(localIP.String(), fmt.Sprint(port)),
		Path:   "/" + name,
	}
	return &fileServer{URL: u.String(), srv: srv}, nil
}

func (fs *fileServer) Close() error {
	return fs.srv.Close()
}
