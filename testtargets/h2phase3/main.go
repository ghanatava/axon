// Standalone Go 1.16+ fixture: two interleaved HTTP/2 streams over real TLS.
// -events records TLS call results for rootless replay testing only. Normal
// operation needs no recorder and can be observed with axon-agent -mode h2.
package main

import (
	"bytes"
	"crypto/tls"
	"encoding/binary"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

const preface = "PRI * HTTP/2.0\r\n\r\nSM\r\n\r\n"

func frame(kind, flags byte, stream uint32, payload []byte) []byte {
	b := make([]byte, 9+len(payload))
	n := len(payload)
	b[0] = byte(n >> 16)
	b[1] = byte(n >> 8)
	b[2] = byte(n)
	b[3] = kind
	b[4] = flags
	binary.BigEndian.PutUint32(b[5:], stream)
	copy(b[9:], payload)
	return b
}
func traffic() ([]byte, []byte) {
	var request, response bytes.Buffer
	request.WriteString(preface)
	for _, f := range [][]byte{
		frame(4, 0, 0, nil), frame(1, 0, 1, []byte{0x83}), frame(9, 4, 1, []byte{0x87, 0x84}), frame(1, 4, 3, []byte{0x83, 0x87, 0x84}),
		frame(0, 0, 1, bytes.Repeat([]byte("a"), 10000)), frame(0, 0, 3, bytes.Repeat([]byte("b"), 9000)),
		frame(0, 1, 1, []byte("end-one")), frame(0, 1, 3, []byte("end-three")), frame(6, 0, 0, []byte("pingpong")),
	} {
		request.Write(f)
	}
	for _, f := range [][]byte{
		frame(4, 0, 0, nil), frame(1, 4, 3, []byte{0x88}), frame(1, 4, 1, []byte{0x88}),
		frame(0, 1, 3, []byte("response-three")), frame(0, 1, 1, []byte("response-one")), frame(6, 1, 0, []byte("pingpong")),
	} {
		response.Write(f)
	}
	return request.Bytes(), response.Bytes()
}

type recorder struct {
	mu      sync.Mutex
	encoder *json.Encoder
	clock   uint64
}
type recordedConn struct {
	*tls.Conn
	id       uint64
	recorder *recorder
}

func (c *recordedConn) record(direction uint32, data []byte) error {
	if c.recorder == nil || len(data) == 0 {
		return nil
	}
	r := c.recorder
	r.mu.Lock()
	defer r.mu.Unlock()
	started := atomic.AddUint64(&r.clock, 1)
	for offset := 0; offset < len(data); offset += 4096 {
		end := offset + 4096
		if end > len(data) {
			end = len(data)
		}
		// Recorder identity is synthetic; it is not a substitute for live g/ABI probes.
		event := struct {
			Started, Finished, GoID, Conn, Total uint64
			PID, TID, Direction                  uint32
			Data                                 []byte
			Offset                               uint64
			Chunked                              bool
		}{started, started, c.id*10 + uint64(direction), c.id, uint64(len(data)), uint32(os.Getpid()), 0, direction, data[offset:end], uint64(offset), true}
		if err := r.encoder.Encode(event); err != nil {
			return err
		}
	}
	return nil
}
func (c *recordedConn) Read(data []byte) (int, error) {
	n, err := c.Conn.Read(data)
	if captureErr := c.record(0, data[:n]); captureErr != nil {
		return n, captureErr
	}
	return n, err
}
func (c *recordedConn) Write(data []byte) (int, error) {
	n, err := c.Conn.Write(data)
	if captureErr := c.record(1, data[:n]); captureErr != nil {
		return n, captureErr
	}
	return n, err
}
func writeFragments(w io.Writer, data []byte) error {
	sizes := []int{1, 2, 5, 7, 8192, 3, 4096, 16384}
	for i := 0; len(data) > 0; i++ {
		n := sizes[i%len(sizes)]
		if n > len(data) {
			n = len(data)
		}
		written, err := w.Write(data[:n])
		if err != nil {
			return err
		}
		if written != n {
			return io.ErrShortWrite
		}
		data = data[n:]
	}
	return nil
}
func readExpected(r io.Reader, want []byte) error {
	var got bytes.Buffer
	b := make([]byte, 257)
	for got.Len() < len(want) {
		n, err := r.Read(b)
		got.Write(b[:n])
		if err != nil {
			return err
		}
	}
	if !bytes.Equal(got.Bytes(), want) {
		return fmt.Errorf("TLS plaintext differs from fixture")
	}
	return nil
}
func run() error {
	events := flag.String("events", "", "optional JSONL TLS recorder for rootless replay")
	certPath := flag.String("cert", "testtargets/certs/cert.pem", "certificate")
	keyPath := flag.String("key", "testtargets/certs/key.pem", "private key")
	flag.Parse()
	var record *recorder
	if *events != "" {
		file, err := os.Create(*events)
		if err != nil {
			return err
		}
		defer file.Close()
		record = &recorder{encoder: json.NewEncoder(file)}
	}
	cert, err := tls.LoadX509KeyPair(*certPath, *keyPath)
	if err != nil {
		return err
	}
	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"h2"}})
	if err != nil {
		return err
	}
	defer listener.Close()
	request, response := traffic()
	serverResult := make(chan error, 1)
	go func() {
		serverResult <- func() error {
			raw, err := listener.Accept()
			if err != nil {
				return err
			}
			defer raw.Close()
			raw.SetDeadline(time.Now().Add(10 * time.Second))
			conn := &recordedConn{Conn: raw.(*tls.Conn), id: 2, recorder: record}
			if err := readExpected(conn, request); err != nil {
				return err
			}
			return writeFragments(conn, response)
		}()
	}()
	raw, err := tls.Dial("tcp", listener.Addr().String(), &tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2"}})
	if err != nil {
		return err
	}
	defer raw.Close()
	raw.SetDeadline(time.Now().Add(10 * time.Second))
	client := &recordedConn{Conn: raw, id: 1, recorder: record}
	if err := writeFragments(client, request); err != nil {
		return err
	}
	if err := readExpected(client, response); err != nil {
		return err
	}
	if err := <-serverResult; err != nil {
		return err
	}
	fmt.Println("Phase 3 fixture passed: streams 1 and 3, fragmented headers, coalesced frames, >4KiB TLS writes")
	return nil
}
func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
