// Command airplayer is a command-line AirPlay video client for Apple TV.
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"math/rand"
	"os"
	"os/signal"
	"time"

	"github.com/naoto/airplayer-go/airplay"
)

const version = "0.2.0"

const browseTimeout = 2 * time.Second

const usage = `Usage:
  airplayer play <URI|PATH>... [-r|--repeat] [-s|--shuffle] [-d|--device=N]
      Play video (URI [YouTube] or PATH [local video file, directory])
  airplayer pair [-d|--device=N]
      Pair with an AirPlay device using the PIN shown on its screen
  airplayer devices
      Show AirPlay devices
  airplayer version, --version
      Display version
`

func main() {
	rand.Seed(time.Now().UnixNano())
	os.Exit(run(os.Args[1:]))
}

func run(args []string) int {
	if len(args) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}
	switch args[0] {
	case "play":
		return cmdPlay(args[1:])
	case "pair":
		return cmdPair(args[1:])
	case "devices":
		return cmdDevices()
	case "version", "--version":
		fmt.Println(version)
		return 0
	case "help", "-h", "--help":
		fmt.Print(usage)
		return 0
	default:
		fmt.Fprintf(os.Stderr, "Unknown command: %s\n\n%s", args[0], usage)
		return 2
	}
}

func cmdPlay(args []string) int {
	fs := flag.NewFlagSet("play", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var repeat, shuffle bool
	var deviceNumber int
	fs.BoolVar(&repeat, "repeat", false, "")
	fs.BoolVar(&repeat, "r", false, "")
	fs.BoolVar(&shuffle, "shuffle", false, "")
	fs.BoolVar(&shuffle, "s", false, "")
	fs.IntVar(&deviceNumber, "device", 0, "")
	fs.IntVar(&deviceNumber, "d", 0, "")

	// Allow options before and after targets.
	var targets []string
	for {
		if err := fs.Parse(args); err == flag.ErrHelp {
			fmt.Print(usage)
			return 0
		} else if err != nil {
			fmt.Fprintf(os.Stderr, "%v\n\n%s", err, usage)
			return 2
		}
		if fs.NArg() == 0 {
			break
		}
		targets = append(targets, fs.Arg(0))
		args = fs.Args()[1:]
	}
	if len(targets) == 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}

	playlist := &Playlist{Repeat: repeat, Shuffle: shuffle}
	for _, t := range targets {
		if err := playlist.Add(t); err != nil {
			fmt.Fprintln(os.Stderr, err)
			return 1
		}
	}

	device, err := selectDevice(deviceNumber)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	client, err := connect(device)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	controller := NewController(client, os.Stdout)
	defer controller.Close()

	err = playlist.Each(func(m *Media) error {
		fmt.Println()
		return controller.Play(ctx, m)
	})
	if errors.Is(err, context.Canceled) {
		return 130
	}
	if errors.Is(err, airplay.ErrPairingRequired) {
		fmt.Fprintf(os.Stderr, "%v\nRun `airplayer pair -d %d` first.\n", err, deviceNumber)
		return 1
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	return 0
}

func cmdPair(args []string) int {
	fs := flag.NewFlagSet("pair", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var deviceNumber int
	fs.IntVar(&deviceNumber, "device", 0, "")
	fs.IntVar(&deviceNumber, "d", 0, "")
	if err := fs.Parse(args); err == flag.ErrHelp {
		fmt.Print(usage)
		return 0
	} else if err != nil || fs.NArg() > 0 {
		fmt.Fprint(os.Stderr, usage)
		return 2
	}

	device, err := selectDevice(deviceNumber)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	store, err := loadCredentials()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}

	name := "airplayer"
	if host, err := os.Hostname(); err == nil {
		name = "airplayer@" + host
	}
	creds, err := airplay.Pair(device, name, func() (string, error) {
		fmt.Printf("Enter the PIN shown on %s: ", device.Name)
		pin, err := bufio.NewReader(os.Stdin).ReadString('\n')
		if err == io.EOF && pin != "" {
			err = nil
		}
		return pin, err
	})
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	if err := store.set(device, creds); err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	fmt.Printf("Paired with %s.\n", device.Name)
	return 0
}

func cmdDevices() int {
	devices, err := browse()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	for i, d := range devices {
		info := airplay.Info{Model: "unknown", Version: "unknown", Resolution: "unknown"}
		if client, err := connect(d); err == nil {
			info = client.Info()
			client.Close()
		}
		fmt.Printf("%d: %s (Model: %s, Resolution: %s, Version: %s, IP: %s)\n",
			i, d.Name, info.Model, info.Resolution, info.Version, d.Addr())
	}
	return 0
}

// selectDevice returns the device with the given number, or the first
// device if there is no such number.
func selectDevice(n int) (airplay.Device, error) {
	devices, err := browse()
	if err != nil {
		return airplay.Device{}, err
	}
	if n < 0 || n >= len(devices) {
		fmt.Printf("Device number %d is not found. So choose %s.\n", n, devices[0].Name)
		n = 0
	}
	return devices[n], nil
}

// connect opens a session with d using stored credentials, if any.
func connect(d airplay.Device) (*airplay.Client, error) {
	store, err := loadCredentials()
	if err != nil {
		return nil, err
	}
	return airplay.Connect(d, store.get(d))
}

func browse() ([]airplay.Device, error) {
	devices, err := airplay.Browse(browseTimeout)
	if errors.Is(err, airplay.ErrNoDevices) {
		return nil, errors.New("AirPlay devices not found.")
	}
	return devices, err
}
