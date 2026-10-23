//go:build linux && amd64

package main

import (
	"net"
	"net/http"
	"testing"

	"github.com/ghanatava/axon/internal/metrics"
)

func TestMetricsListenerLifecycle(t *testing.T) {
	// Keep the first socket open to verify a bind failure is returned to run().
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := listener.Addr().String()
	exporter, _ := metrics.New(metrics.Config{})
	if closeServer, err := serveMetrics(address, exporter); err == nil {
		closeServer()
		t.Fatal("occupied address accepted")
	}
	listener.Close()
	closeServer, err := serveMetrics(address, exporter)
	if err != nil {
		t.Fatal(err)
	}
	defer closeServer()
	response, err := http.Get("http://" + address + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	response.Body.Close()
	if response.StatusCode != 200 {
		t.Fatal(response.StatusCode)
	}
	closeServer()
	rebound, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatal("listener leaked", err)
	}
	rebound.Close()
}
