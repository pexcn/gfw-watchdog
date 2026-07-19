package main

import (
	"flag"
	"log"
	"net"
)

func RunEchoServer(addr string) error {
	conn, err := net.ListenPacket("udp", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	buffer := make([]byte, 1500)
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

func main() {
	addr := flag.String("listen", ":9000", "UDP listen address")
	flag.Parse()
	log.Printf("UDP echo server listening on %s", *addr)
	if err := RunEchoServer(*addr); err != nil {
		log.Fatal(err)
	}
}
