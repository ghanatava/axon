//go:build linux && amd64

package main

import (
	"context"
	"log"
	"net"
	"net/http"
	"time"

	"github.com/ghanatava/axon/internal/metrics"
)

func serveMetrics(address string, exporter *metrics.Exporter) (func(), error) {
	mux := http.NewServeMux()
	mux.Handle("/metrics", exporter.Handler())
	return serveHandler(address, mux)
}
func serveHandler(address string, handler http.Handler) (func(), error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, err
	}
	server := &http.Server{Handler: handler, ReadHeaderTimeout: 5 * time.Second, WriteTimeout: 10 * time.Second, IdleTimeout: 30 * time.Second}
	go func() {
		if err := server.Serve(listener); err != nil && err != http.ErrServerClosed {
			log.Printf("metrics server: %v", err)
		}
	}()
	log.Printf("metrics listening on %s", listener.Addr())
	return func() {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		if err := server.Shutdown(ctx); err != nil {
			server.Close()
		}
	}, nil
}
