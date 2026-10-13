package main

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"os"
)

func main() {
	cert, err := tls.LoadX509KeyPair("testtargets/certs/cert.pem", "testtargets/certs/key.pem")
	if err != nil {
		log.Fatalf("loading cert: %v", err)
	}

	config := &tls.Config{Certificates: []tls.Certificate{cert}}
	listener, err := tls.Listen("tcp", "127.0.0.1:8443", config)
	if err != nil {
		log.Fatalf("listening: %v", err)
	}
	defer listener.Close()

	fmt.Printf("pid=%d -- TLS echo server listening on :8443\n", os.Getpid())
	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("accept error: %v", err)
			continue
		}
		go handleCon(conn)
	}
}

func handleCon(conn net.Conn) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			log.Printf("read error: %v", err)
			return
		}
		fmt.Printf("server received: %q\n", line)
		if _, err := conn.Write([]byte(line)); err != nil {
			return
		}
	}
}
