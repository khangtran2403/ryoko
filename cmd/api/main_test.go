package main

import (
	"net/http"
	"testing"
)

func TestNewHTTPServerAppliesSafetyLimits(t *testing.T) {
	handler := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {})
	server := newHTTPServer(":9090", handler)

	if server.Addr != ":9090" {
		t.Errorf("Addr = %q, want %q", server.Addr, ":9090")
	}
	if server.Handler == nil {
		t.Fatal("Handler is nil")
	}
	if server.ReadHeaderTimeout != serverReadHeaderTimeout {
		t.Errorf("ReadHeaderTimeout = %v, want %v", server.ReadHeaderTimeout, serverReadHeaderTimeout)
	}
	if server.ReadTimeout != serverReadTimeout {
		t.Errorf("ReadTimeout = %v, want %v", server.ReadTimeout, serverReadTimeout)
	}
	if server.WriteTimeout != serverWriteTimeout {
		t.Errorf("WriteTimeout = %v, want %v", server.WriteTimeout, serverWriteTimeout)
	}
	if server.IdleTimeout != serverIdleTimeout {
		t.Errorf("IdleTimeout = %v, want %v", server.IdleTimeout, serverIdleTimeout)
	}
	if server.MaxHeaderBytes != serverMaxHeaderBytes {
		t.Errorf("MaxHeaderBytes = %d, want %d", server.MaxHeaderBytes, serverMaxHeaderBytes)
	}
}

func TestServerSafetyLimitsArePositive(t *testing.T) {
	if serverReadHeaderTimeout <= 0 || serverReadTimeout <= 0 ||
		serverWriteTimeout <= 0 || serverIdleTimeout <= 0 {
		t.Fatal("all HTTP server timeouts must be positive")
	}
	if serverMaxHeaderBytes <= 0 {
		t.Fatal("HTTP maximum header size must be positive")
	}
}
