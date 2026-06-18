package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestResolveComponentMetadataUsesExplicitVersion(t *testing.T) {
	t.Parallel()

	config, err := resolveComponentMetadata(context.Background(), Config{
		ComponentVersion:    "v1.2.3",
		ComponentVersionURL: "https://region.example/api/version",
	})
	if err != nil {
		t.Fatalf("resolveComponentMetadata returned error: %v", err)
	}
	if config.ComponentVersion != "v1.2.3" {
		t.Fatalf("component version = %q", config.ComponentVersion)
	}
}

func TestResolveComponentMetadataFetchesVersionEndpoint(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/api/version" {
			t.Fatalf("unexpected path: %s", request.URL.Path)
		}
		if request.Header.Get("Authorization") != "Bearer token-123" {
			t.Fatalf("authorization header = %q", request.Header.Get("Authorization"))
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"version":"v1.2.3"}`))
	}))
	defer server.Close()

	config, err := resolveComponentMetadata(context.Background(), Config{
		ComponentVersionURL:     server.URL + "/api/version",
		ComponentVersionToken:   "token-123",
		ComponentVersionTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("resolveComponentMetadata returned error: %v", err)
	}
	if config.ComponentVersion != "v1.2.3" {
		t.Fatalf("component version = %q", config.ComponentVersion)
	}
}

func TestComponentVersionFromJSONSupportsNestedVersion(t *testing.T) {
	t.Parallel()

	version, err := componentVersionFromJSON([]byte(`{"metadata":{"version":"v2.0.0"}}`))
	if err != nil {
		t.Fatalf("componentVersionFromJSON returned error: %v", err)
	}
	if version != "v2.0.0" {
		t.Fatalf("version = %q", version)
	}
}
