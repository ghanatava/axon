// Cluster traffic fixture. /run opens a NEW TLS connection so discovery does
// not require tracing an already established HTTP/2 connection.
package main

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	grpcgo "google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/credentials"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/wrapperspb"
	"log"
	"net"
	"net/http"
	"os"
	"strconv"
	"sync"
	"syscall"
	"time"
)

type echoService interface {
	Unary(context.Context, *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error)
}
type echo struct{}

func (echo) Unary(ctx context.Context, in *wrapperspb.BytesValue) (*wrapperspb.BytesValue, error) {
	if len(in.Value) > 0 && string(in.Value[len(in.Value)-1:]) == "!" {
		return nil, status.Error(codes.InvalidArgument, "fixture error")
	}
	time.Sleep(2 * time.Millisecond)
	return in, nil
}

var service = grpcgo.ServiceDesc{ServiceName: "axon.phase7.Echo", HandlerType: (*echoService)(nil), Methods: []grpcgo.MethodDesc{{MethodName: "Unary", Handler: func(srv any, ctx context.Context, decode func(any) error, interceptor grpcgo.UnaryServerInterceptor) (any, error) {
	in := new(wrapperspb.BytesValue)
	if err := decode(in); err != nil {
		return nil, err
	}
	return srv.(echoService).Unary(ctx, in)
}}}}

type expectation struct {
	Token  string `json:"token"`
	Status int    `json:"status"`
}

func main() {
	cert, err := tls.LoadX509KeyPair("/etc/axon/cert.pem", "/etc/axon/key.pem")
	if err != nil {
		log.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		log.Fatal(err)
	}
	server := grpcgo.NewServer(grpcgo.Creds(credentials.NewTLS(&tls.Config{Certificates: []tls.Certificate{cert}, MinVersion: tls.VersionTLS12})))
	server.RegisterService(&service, echo{})
	go server.Serve(listener)
	var mu sync.Mutex
	round := 0
	instance := strconv.FormatInt(time.Now().UnixNano(), 10)
	http.HandleFunc("/ready", func(w http.ResponseWriter, r *http.Request) { fmt.Fprintln(w, "ok") })
	http.HandleFunc("/restart-runtime", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST required", 405)
			return
		}
		fmt.Fprintln(w, "executing self")
		go func() {
			time.Sleep(100 * time.Millisecond)
			if err := syscall.Exec("/fixture", os.Args, os.Environ()); err != nil {
				log.Fatal(err)
			}
		}()
	})
	http.HandleFunc("/run", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" {
			http.Error(w, "POST required", 405)
			return
		}
		count, _ := strconv.Atoi(r.URL.Query().Get("count"))
		if count < 1 || count > 100 {
			count = 10
		}
		mu.Lock()
		defer mu.Unlock()
		round++
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		conn, err := grpcgo.DialContext(ctx, listener.Addr().String(), grpcgo.WithTransportCredentials(credentials.NewTLS(&tls.Config{InsecureSkipVerify: true})), grpcgo.WithBlock())
		if err != nil {
			http.Error(w, err.Error(), 500)
			return
		}
		defer conn.Close()
		expected := []expectation{}
		for n := 0; n < count; n++ {
			token := fmt.Sprintf("%s/i%s/r%d/n%d", os.Getenv("POD_NAME"), instance, round, n)
			want := 0
			if n%5 == 4 {
				token += "!"
				want = 3
			}
			var out wrapperspb.BytesValue
			err := conn.Invoke(ctx, "/axon.phase7.Echo/Unary", wrapperspb.Bytes([]byte(token)), &out)
			if int(status.Code(err)) != want || want == 0 && string(out.Value) != token {
				http.Error(w, fmt.Sprintf("unexpected RPC result %v", err), 500)
				return
			}
			expected = append(expected, expectation{token, want})
		}
		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(struct {
			Pod   string        `json:"pod"`
			Calls []expectation `json:"calls"`
		}{os.Getenv("POD_NAME"), expected})
	})
	log.Fatal(http.ListenAndServe(":19090", nil))
}
