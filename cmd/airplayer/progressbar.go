package main

import (
	"fmt"
	"io"
	"strings"
	"time"
)

const progressBarWidth = 46

// progressBar renders a single line like
//
//	Time: 00:04:25 |=                                              | 3% Streaming
type progressBar struct {
	w       io.Writer
	start   time.Time
	percent float64
	title   string
}

func newProgressBar(w io.Writer) *progressBar {
	b := &progressBar{w: w, start: time.Now(), title: "Waiting"}
	b.render()
	return b
}

func (b *progressBar) Update(title string, percent float64) {
	b.title = title
	b.percent = percent
	b.render()
}

func (b *progressBar) Finish() {
	b.Update("Complete", 100)
	fmt.Fprintln(b.w)
}

func (b *progressBar) render() {
	p := b.percent
	if p < 0 {
		p = 0
	} else if p > 100 {
		p = 100
	}
	filled := int(p / 100 * progressBarWidth)
	fmt.Fprintf(b.w, "\r   Time: %s |%s%s| %d%% %-9s",
		formatDuration(time.Since(b.start)),
		strings.Repeat("=", filled),
		strings.Repeat(" ", progressBarWidth-filled),
		int(p), b.title)
}

func formatDuration(d time.Duration) string {
	s := int(d.Seconds())
	return fmt.Sprintf("%02d:%02d:%02d", s/3600, s/60%60, s%60)
}
