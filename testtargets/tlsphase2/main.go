// Standalone fixture: build this file with Go 1.16 as well as the current Go.
package main

import (
	"bytes"
	"crypto/tls"
	"flag"
	"fmt"
	"io"
	"log"
	"runtime"
	"sync"
)

func main() {
	certPath := flag.String("cert", "testtargets/certs/cert.pem", "certificate")
	keyPath := flag.String("key", "testtargets/certs/key.pem", "private key")
	flag.Parse()
	cert, err := tls.LoadX509KeyPair(*certPath, *keyPath)
	if err != nil {
		log.Fatal(err)
	}
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"h2"}})
	if err != nil {
		log.Fatal(err)
	}
	defer listener.Close()
	const clients = 4
	errs := make(chan error, clients*2)
	var workers sync.WaitGroup
	workers.Add(clients * 2)
	go func() {
		for i := 0; i < clients; i++ {
			conn, err := listener.Accept()
			if err != nil {
				errs <- err
				workers.Done()
				continue
			}
			go func() {
				defer workers.Done()
				defer conn.Close()
				_, err := io.Copy(conn, conn)
				if err != nil {
					errs <- err
				}
			}()
		}
	}()
	for i := 0; i < clients; i++ {
		go func() {
			defer workers.Done()
			conn, err := tls.Dial("tcp", listener.Addr().String(), &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2"}})
			if err != nil {
				errs <- err
				return
			}
			defer conn.Close()
			// The wire preface is the Phase 2 exit criterion; this fixture echoes bytes,
			// rather than implementing the HTTP/2 framing assigned to Phase 3.
			messages := [][]byte{[]byte("PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"), []byte("axon-phase2-plaintext"), bytes.Repeat([]byte("x"), 8192)}
			for repeat := 0; repeat < 10; repeat++ {
				for _, message := range messages {
					runtime.Gosched()
					if _, err = conn.Write(message); err != nil {
						errs <- err
						return
					}
					reply := make([]byte, len(message))
					if _, err = io.ReadFull(conn, reply); err != nil {
						errs <- err
						return
					}
					if !bytes.Equal(message, reply) {
						errs <- fmt.Errorf("echo mismatch")
						return
					}
				}
			}
		}()
	}
	workers.Wait()
	close(errs)
	for err := range errs {
		log.Fatal(err)
	}
	fmt.Println("TLS plaintext fixture passed: 4 concurrent connections, 120 exchanges")
}
