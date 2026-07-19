package main

import (
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"strings"
)

const maxUDPDatagramSize = 64 * 1024

func RunEchoServer(addr string) error {
	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	buffer := make([]byte, maxUDPDatagramSize)
	for {
		n, remote, err := conn.ReadFrom(buffer)
		if err != nil {
			log.Printf("read failed: %v", err)
			continue
		}
		if _, err := conn.WriteTo(buffer[:n], remote); err != nil {
			log.Printf("write to %s failed: %v", remote, err)
		}
	}
}

func wantsHelp(args []string) bool {
	for _, arg := range args {
		if arg == "-h" || arg == "--help" {
			return true
		}
	}
	return false
}

func printUsage(w io.Writer) {
	const optionWidth = 24
	fmt.Fprintln(w, "Run a UDP echo server for reachability diagnostics.")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Usage:")
	fmt.Fprintln(w, "  echo-server [options]")
	fmt.Fprintln(w)
	fmt.Fprintln(w, "Options:")
	fmt.Fprintf(w, "  %-*s %s\n", optionWidth, "-l, --listen ADDRESS", "UDP listen address (default :9000)")
	fmt.Fprintf(w, "  %-*s %s\n", optionWidth, "-h, --help", "Show this help message and exit")
}

func main() {
	if wantsHelp(os.Args[1:]) {
		printUsage(os.Stdout)
		return
	}
	fs := flag.NewFlagSet("echo-server", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	var addr string
	fs.StringVar(&addr, "listen", ":9000", "UDP listen address")
	fs.StringVar(&addr, "l", ":9000", "UDP listen address")
	if err := fs.Parse(os.Args[1:]); err != nil {
		log.Print(err)
		os.Exit(2)
	}
	if fs.NArg() != 0 {
		log.Printf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
		os.Exit(2)
	}
	log.Printf("UDP echo server listening on %s", addr)
	if err := RunEchoServer(addr); err != nil {
		log.Fatal(err)
	}
}
