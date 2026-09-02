package main

import (
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestCheckHealthURL(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusOK)
	}))
	defer healthy.Close()
	if err := checkHealthURL(healthy.URL); err != nil {
		t.Fatalf("checkHealthURL(healthy) error = %v", err)
	}

	unhealthy := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.WriteHeader(http.StatusServiceUnavailable)
	}))
	if err := checkHealthURL(unhealthy.URL); err == nil {
		t.Fatal("checkHealthURL(unhealthy) unexpectedly succeeded")
	}
	unhealthy.Close()
	if err := checkHealthURL(unhealthy.URL); err == nil {
		t.Fatal("checkHealthURL(unavailable) unexpectedly succeeded")
	}
}
