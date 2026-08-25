package user

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestGetOrFetchKeyPairContextCancelsRequest(t *testing.T) {
	requestStarted := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		close(requestStarted)
		<-r.Context().Done()
	}))
	defer server.Close()

	oldURL, oldClient := ServicesURL, client
	ServicesURL, client = server.URL, server.Client()
	defer func() {
		ServicesURL, client = oldURL, oldClient
	}()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := GetOrFetchKeyPairContext(ctx, "token")
		done <- err
	}()
	select {
	case <-requestStarted:
	case <-time.After(time.Second):
		t.Fatal("certificate request did not start")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("GetOrFetchKeyPairContext() error = %v; want context.Canceled", err)
		}
	case <-time.After(time.Second):
		t.Fatal("certificate request ignored context cancellation")
	}
}
