package main

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"log"
	"os"
	"time"
)

func main() {
	config := &tls.Config{InsecureSkipVerify: true}

	conn, err := tls.Dial("tcp", "127.0.0.1:8443", config)
	if err != nil {
		log.Fatalf("dialing: %v", err)
	}
	defer conn.Close()

	fmt.Printf("pid=%d -- connected, sending messages in a loop\n", os.Getpid())

	reader := bufio.NewReader(conn)
	messages := []string{"hello\n", "phase two\n", "tls interception\n"}

	for {
		for _, msg := range messages {
			if _, err := conn.Write([]byte(msg)); err != nil {
				log.Fatalf("write error: %v", err)
			}
			reply, err := reader.ReadString('\n')
			if err != nil {
				log.Fatalf("read error: %v", err)
			}
			fmt.Printf("client sent %q, got back %q\n", msg, reply)
			time.Sleep(1 * time.Second)
		}
	}
}
