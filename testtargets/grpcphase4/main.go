// Real grpc-go client/server over TLS. The optional recorder is exclusively a
// rootless test input source; live uprobes work with the recorder disabled.
package main

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"sync"
	"time"

	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	_ "google.golang.org/grpc/encoding/gzip"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
)

type echoService interface {
	Unary(context.Context, *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error)
	Bidi(grpcgo.ServerStream) error
}
type echo struct{}

func (echo) Unary(ctx context.Context, input *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error) {
	if string(input.Value) == "fail" {
		return nil, status.Error(codes.InvalidArgument, "bad input + details")
	}
	select {
	case <-time.After(10 * time.Millisecond):
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return wrapperspb.Bytes(input.Value), nil
}
func (echo) Bidi(stream grpcgo.ServerStream) error {
	for {
		var input wrapperspb.BytesValue
		if err := stream.RecvMsg(&input); err == io.EOF {
			return nil
		} else if err != nil {
			return err
		}
		if err := stream.SendMsg(wrapperspb.Bytes(input.Value)); err != nil {
			return err
		}
	}
}

var service = grpcgo.ServiceDesc{ServiceName: "axon.demo.Echo", HandlerType: (*echoService)(nil), Methods: []grpcgo.MethodDesc{{MethodName: "Unary", Handler: func(srv any, ctx context.Context, decode func(any) error, interceptor grpcgo.UnaryServerInterceptor) (any, error) {
	var input wrapperspb.BytesValue
	if err := decode(&input); err != nil {
		return nil, err
	}
	if interceptor == nil {
		return srv.(echoService).Unary(ctx, &input)
	}
	return interceptor(ctx, &input, &grpcgo.UnaryServerInfo{Server: srv, FullMethod: "/axon.demo.Echo/Unary"}, func(ctx context.Context, request any) (any, error) {
		return srv.(echoService).Unary(ctx, request.(*wrapperspb.BytesValue))
	})
}}}, Streams: []grpcgo.StreamDesc{{StreamName: "Bidi", Handler: func(srv any, stream grpcgo.ServerStream) error { return srv.(echoService).Bidi(stream) }, ServerStreams: true, ClientStreams: true}}}

type recorder struct {
	mu      sync.Mutex
	encoder *json.Encoder
	origin  time.Time
	closed  bool
}
type recordingConn struct {
	net.Conn
	id       uint64
	recorder *recorder
}

func (c *recordingConn) record(direction uint32, data []byte, started, finished uint64) error {
	if len(data) == 0 {
		return nil
	}
	r := c.recorder
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return nil
	}
	for offset := 0; offset < len(data); offset += 4096 {
		end := offset + 4096
		if end > len(data) {
			end = len(data)
		}
		event := struct {
			Started, Finished, GoID, Conn, Total uint64
			PID, TID, Direction                  uint32
			Data                                 []byte
			Offset                               uint64
			Chunked                              bool
		}{started, finished, c.id*10 + uint64(direction), c.id, uint64(len(data)), uint32(os.Getpid()), 0, direction, data[offset:end], uint64(offset), true}
		if err := r.encoder.Encode(event); err != nil {
			return err
		}
	}
	return nil
}
func (c *recordingConn) Read(data []byte) (int, error) {
	start := uint64(time.Since(c.recorder.origin).Nanoseconds()) + 1
	n, err := c.Conn.Read(data)
	end := uint64(time.Since(c.recorder.origin).Nanoseconds()) + 1
	if e := c.record(0, data[:n], start, end); e != nil {
		return n, e
	}
	return n, err
}
func (c *recordingConn) Write(data []byte) (int, error) {
	start := uint64(time.Since(c.recorder.origin).Nanoseconds()) + 1
	n, err := c.Conn.Write(data)
	end := uint64(time.Since(c.recorder.origin).Nanoseconds()) + 1
	if e := c.record(1, data[:n], start, end); e != nil {
		return n, e
	}
	return n, err
}

type recordingCredentials struct {
	credentials.TransportCredentials
	recorder *recorder
}

func (c *recordingCredentials) ClientHandshake(ctx context.Context, authority string, conn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	tlsConn, auth, err := c.TransportCredentials.ClientHandshake(ctx, authority, conn)
	if err != nil {
		return nil, nil, err
	}
	return &recordingConn{tlsConn, 1, c.recorder}, auth, nil
}
func (c *recordingCredentials) ServerHandshake(conn net.Conn) (net.Conn, credentials.AuthInfo, error) {
	tlsConn, auth, err := c.TransportCredentials.ServerHandshake(conn)
	if err != nil {
		return nil, nil, err
	}
	return &recordingConn{tlsConn, 2, c.recorder}, auth, nil
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
func run() error {
	events := flag.String("events", "", "optional TLS event JSONL recorder for rootless tests")
	certPath := flag.String("cert", "testtargets/certs/cert.pem", "certificate")
	keyPath := flag.String("key", "testtargets/certs/key.pem", "key")
	flag.Parse()
	var record *recorder
	if *events != "" {
		file, err := os.Create(*events)
		if err != nil {
			return err
		}
		record = &recorder{encoder: json.NewEncoder(file), origin: time.Now()}
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
	serveDone := make(chan struct{})
	go func() { defer close(serveDone); server.Serve(listener) }()
	defer func() { server.Stop(); <-serveDone }()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	client, err := grpcgo.DialContext(ctx, listener.Addr().String(), grpcgo.WithTransportCredentials(creds(&tls.Config{InsecureSkipVerify: true, NextProtos: []string{"h2"}}, record)), grpcgo.WithBlock())
	if err != nil {
		return err
	}
	defer client.Close()
	payloads := [][]byte{[]byte("hello-phase4"), bytes.Repeat([]byte("large-message-"), 1000), bytes.Repeat([]byte("compress-me-"), 700), []byte("fail")}
	errors := make(chan error, 5)
	start := make(chan struct{})
	var workers sync.WaitGroup
	for i, payload := range payloads {
		i, payload := i, payload
		workers.Add(1)
		go func() {
			defer workers.Done()
			<-start
			var response wrapperspb.BytesValue
			var options []grpcgo.CallOption
			if i == 2 {
				options = append(options, grpcgo.UseCompressor("gzip"))
			}
			err := client.Invoke(ctx, "/axon.demo.Echo/Unary", wrapperspb.Bytes(payload), &response, options...)
			if i == 3 {
				if status.Code(err) != codes.InvalidArgument {
					errors <- fmt.Errorf("expected status3, got %v", err)
				}
				return
			}
			if err != nil {
				errors <- err
				return
			}
			if !bytes.Equal(response.Value, payload) {
				errors <- fmt.Errorf("unary payload mismatch")
			}
		}()
	}
	workers.Add(1)
	go func() {
		defer workers.Done()
		<-start
		stream, err := client.NewStream(ctx, &grpcgo.StreamDesc{ServerStreams: true, ClientStreams: true}, "/axon.demo.Echo/Bidi")
		if err != nil {
			errors <- err
			return
		}
		for _, payload := range [][]byte{[]byte("one"), bytes.Repeat([]byte("two"), 1000), []byte("three")} {
			if err := stream.SendMsg(wrapperspb.Bytes(payload)); err != nil {
				errors <- err
				return
			}
			var reply wrapperspb.BytesValue
			if err := stream.RecvMsg(&reply); err != nil {
				errors <- err
				return
			}
			if !bytes.Equal(reply.Value, payload) {
				errors <- fmt.Errorf("bidi payload mismatch")
				return
			}
		}
		if err := stream.CloseSend(); err != nil {
			errors <- err
			return
		}
		var end wrapperspb.BytesValue
		if err := stream.RecvMsg(&end); err != io.EOF {
			errors <- fmt.Errorf("expected streaming EOF, got %v", err)
		}
	}()
	close(start)
	workers.Wait()
	close(errors)
	for err := range errors {
		return err
	}
	fmt.Println("Phase 4 grpc-go TLS fixture passed: 4 concurrent unary calls (large, gzip, status3), 1 bidirectional stream")
	return nil
}
func main() {
	if err := run(); err != nil {
		log.Fatal(err)
	}
}
