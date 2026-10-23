// Phase 5 load fixture: concurrent streams, multiple TLS connections, churn and
// GC. -events is a rootless test recorder; omit it for uninstrumented live BPF.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"runtime"
	"sync"
	"sync/atomic"
	"time"

	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/credentials"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type echoService interface {
	Unary(context.Context, *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error)
}
type echo struct{}

//go:noinline
func grow(depth int) int {
	var block [512]byte
	block[depth%len(block)] = byte(depth)
	n := int(block[depth%len(block)])
	if depth > 0 {
		n += grow(depth - 1)
	}
	runtime.KeepAlive(&block)
	return n
}
func (echo) Unary(_ context.Context, input *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error) {
	grow(12)
	runtime.Gosched()
	return wrapperspb.Bytes(input.Value), nil
}

var service = grpcgo.ServiceDesc{ServiceName: "axon.load.Echo", HandlerType: (*echoService)(nil), Methods: []grpcgo.MethodDesc{{MethodName: "Unary", Handler: func(srv any, ctx context.Context, decode func(any) error, _ grpcgo.UnaryServerInterceptor) (any, error) {
	var input wrapperspb.BytesValue
	if err := decode(&input); err != nil {
		return nil, err
	}
	return srv.(echoService).Unary(ctx, &input)
}}}}

type event struct {
	Started, Finished, GoID, Conn, Total uint64
	PID, TID, Direction                  uint32
	Data                                 []byte
	Offset                               uint64
	Chunked                              bool
	ProcessStart, Generation, Sequence   uint64
	Kind, Failure, Version               uint32
}
type recorder struct {
	mu                            sync.Mutex
	encoder                       *json.Encoder
	origin                        time.Time
	birth, ids, events, dropEvery uint64
	closed                        bool
}
type recordedConn struct {
	net.Conn
	recorder *recorder
	id       uint64
	sequence [2]uint64
	once     sync.Once
}

func (c *recordedConn) record(direction uint32, data []byte, started, finished uint64) error {
	if len(data) == 0 {
		return nil
	}
	r := c.recorder
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	c.sequence[direction]++
	for offset := 0; offset < len(data); offset += 4096 {
		end := offset + 4096
		if end > len(data) {
			end = len(data)
		}
		e := event{Started: started, Finished: finished, GoID: c.id*10 + uint64(direction), Conn: c.id*4096 + 1, Total: uint64(len(data)), PID: uint32(os.Getpid()), TID: uint32(os.Getpid()), Direction: direction, Data: data[offset:end], Offset: uint64(offset), Chunked: true, ProcessStart: r.birth, Generation: c.id, Sequence: c.sequence[direction], Version: 3}
		r.events++
		if r.dropEvery > 0 && r.events%r.dropEvery == 0 {
			continue
		}
		if err := r.encoder.Encode(e); err != nil {
			return err
		}
	}
	return nil
}
func (c *recordedConn) Read(data []byte) (int, error) {
	s := uint64(time.Since(c.recorder.origin).Nanoseconds()) + 1
	n, err := c.Conn.Read(data)
	if e := c.record(0, data[:n], s, uint64(time.Since(c.recorder.origin).Nanoseconds())+1); e != nil {
		return n, e
	}
	return n, err
}
func (c *recordedConn) Write(data []byte) (int, error) {
	s := uint64(time.Since(c.recorder.origin).Nanoseconds()) + 1
	n, err := c.Conn.Write(data)
	if e := c.record(1, data[:n], s, uint64(time.Since(c.recorder.origin).Nanoseconds())+1); e != nil {
		return n, e
	}
	return n, err
}
func (c *recordedConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() {
		r := c.recorder
		r.mu.Lock()
		defer r.mu.Unlock()
		if !r.closed {
			r.encoder.Encode(event{Started: uint64(time.Since(r.origin).Nanoseconds()) + 1, Conn: c.id*4096 + 1, PID: uint32(os.Getpid()), Chunked: true, ProcessStart: r.birth, Generation: c.id, Kind: 1, Version: 3})
		}
	})
	return err
}

type recordingCredentials struct {
	credentials.TransportCredentials
	recorder *recorder
}

func (c *recordingCredentials) wrap(conn net.Conn) net.Conn {
	id := atomic.AddUint64(&c.recorder.ids, 1)
	return &recordedConn{Conn: conn, recorder: c.recorder, id: id}
}
func (c *recordingCredentials) ClientHandshake(ctx context.Context, authority string, raw net.Conn) (net.Conn, credentials.AuthInfo, error) {
	conn, auth, err := c.TransportCredentials.ClientHandshake(ctx, authority, raw)
	if err != nil {
		return nil, nil, err
	}
	return c.wrap(conn), auth, nil
}
func (c *recordingCredentials) ServerHandshake(raw net.Conn) (net.Conn, credentials.AuthInfo, error) {
	conn, auth, err := c.TransportCredentials.ServerHandshake(raw)
	if err != nil {
		return nil, nil, err
	}
	return c.wrap(conn), auth, nil
}
func (c *recordingCredentials) Clone() credentials.TransportCredentials {
	return &recordingCredentials{c.TransportCredentials.Clone(), c.recorder}
}
func creds(config *tls.Config, r *recorder) credentials.TransportCredentials {
	base := credentials.NewTLS(config)
	if r != nil {
		return &recordingCredentials{base, r}
	}
	return base
}

type manifest struct {
	PID         int      `json:"pid"`
	Worker      string   `json:"worker"`
	Tokens      []string `json:"tokens"`
	Connections int      `json:"connections"`
	DurationNS  int64    `json:"duration_ns"`
}

func run() error {
	worker := flag.String("worker", "worker", "unique load identity")
	connections := flag.Int("connections", 3, "concurrent TLS connections per round")
	calls := flag.Int("calls", 40, "calls per connection per round")
	parallel := flag.Int("parallel", 2, "concurrent streams per connection")
	rounds := flag.Int("rounds", 3, "connection churn rounds (ignored with duration)")
	duration := flag.Duration("duration", 0, "repeat full rounds for at least this duration")
	pause := flag.Duration("pause", time.Millisecond, "pause per completed call")
	events := flag.String("events", "", "optional rootless TLS recorder JSONL")
	manifestPath := flag.String("manifest", "", "expected call identities JSON")
	dropEvery := flag.Uint64("drop-every", 0, "recorder only: omit every Nth data event")
	certPath := flag.String("cert", "testtargets/certs/cert.pem", "certificate")
	keyPath := flag.String("key", "testtargets/certs/key.pem", "key")
	flag.Parse()
	if *connections < 1 || *calls < 1 || *parallel < 1 || *rounds < 1 || *duration < 0 || *pause < 0 {
		return fmt.Errorf("load bounds must be positive")
	}
	var record *recorder
	if *events != "" {
		file, err := os.Create(*events)
		if err != nil {
			return err
		}
		record = &recorder{encoder: json.NewEncoder(file), origin: time.Now(), birth: uint64(time.Now().UnixNano()), dropEvery: *dropEvery}
		defer func() { record.mu.Lock(); record.closed = true; file.Close(); record.mu.Unlock() }()
	}
	cert, err := tls.LoadX509KeyPair(*certPath, *keyPath)
	if err != nil {
		return err
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return err
	}
	server := grpcgo.NewServer(grpcgo.Creds(creds(&tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: []string{"h2"}}, record)))
	server.RegisterService(&service, echo{})
	done := make(chan struct{})
	go func() { defer close(done); server.Serve(listener) }()
	defer func() { server.Stop(); <-done }()
	started := time.Now()
	result := manifest{PID: os.Getpid(), Worker: *worker}
	var resultMu sync.Mutex
	for round := 0; (*duration > 0 && time.Since(started) < *duration) || (*duration == 0 && round < *rounds); round++ {
		var clients sync.WaitGroup
		failures := make(chan error, *connections)
		for ci := 0; ci < *connections; ci++ {
			ci := ci
			clients.Add(1)
			go func() {
				defer clients.Done()
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()
				client, err := grpcgo.DialContext(ctx, listener.Addr().String(), grpcgo.WithTransportCredentials(creds(&tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2"}}, record)), grpcgo.WithBlock(), grpcgo.WithDisableRetry())
				if err != nil {
					failures <- err
					return
				}
				defer client.Close()
				resultMu.Lock()
				result.Connections++
				resultMu.Unlock()
				var next atomic.Int64
				var streams sync.WaitGroup
				errors := make(chan error, *parallel)
				for p := 0; p < *parallel; p++ {
					streams.Add(1)
					go func() {
						defer streams.Done()
						for {
							index := int(next.Add(1)) - 1
							if index >= *calls {
								return
							}
							token := fmt.Sprintf("%s/r%d/c%d/n%d", *worker, round, ci, index)
							payload := []byte(token + "|" + string(bytes.Repeat([]byte("x"), 96)))
							var reply wrapperspb.BytesValue
							if err := client.Invoke(ctx, "/axon.load.Echo/Unary", wrapperspb.Bytes(payload), &reply); err != nil {
								errors <- err
								return
							}
							if !bytes.Equal(payload, reply.Value) {
								errors <- fmt.Errorf("payload mismatch for %s", token)
								return
							}
							resultMu.Lock()
							result.Tokens = append(result.Tokens, token)
							resultMu.Unlock()
							if *pause > 0 {
								time.Sleep(*pause)
							}
						}
					}()
				}
				streams.Wait()
				close(errors)
				for err := range errors {
					failures <- err
					return
				}
			}()
		}
		clients.Wait()
		close(failures)
		for err := range failures {
			return err
		}
		if round%2 == 0 {
			runtime.GC()
		}
	}
	result.DurationNS = time.Since(started).Nanoseconds()
	if *manifestPath != "" {
		file, err := os.Create(*manifestPath)
		if err != nil {
			return err
		}
		err = json.NewEncoder(file).Encode(result)
		closeErr := file.Close()
		if err != nil {
			return err
		}
		if closeErr != nil {
			return closeErr
		}
	}
	fmt.Printf("Phase5 worker=%s pid=%d calls=%d connections=%d duration=%s\n", *worker, result.PID, len(result.Tokens), result.Connections, time.Duration(result.DurationNS))
	return nil
}
func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
