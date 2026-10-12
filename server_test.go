package main

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/mark3labs/mcp-go/mcp"
)

func callTool(t *testing.T, handler func(context.Context, mcp.CallToolRequest) (*mcp.CallToolResult, error), args map[string]any) map[string]any {
	t.Helper()
	req := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: args}}
	res, err := handler(context.Background(), req)
	if err != nil {
		t.Fatalf("handler returned error: %v", err)
	}
	if res.IsError {
		t.Fatalf("tool returned error result: %+v", res.Content)
	}
	for _, c := range res.Content {
		if tc, ok := c.(mcp.TextContent); ok {
			var out map[string]any
			if err := json.Unmarshal([]byte(tc.Text), &out); err != nil {
				t.Fatalf("result is not JSON: %s", tc.Text)
			}
			return out
		}
	}
	t.Fatal("tool result has no text content")
	return nil
}

func TestHTTPRequest(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("X-Test", "yes")
		w.WriteHeader(http.StatusCreated)
		_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "query": r.URL.Query().Get("y")})
	}))
	defer srv.Close()

	out := callTool(t, handleHTTPRequest, map[string]any{
		"method":  "post",
		"url":     srv.URL + "/v1/items?x=1",
		"headers": map[string]any{"X-Req": "1"},
		"query":   map[string]any{"y": "2"},
		"body":    `{"hello":1}`,
	})

	if out["status"].(float64) != http.StatusCreated {
		t.Fatalf("status = %v, want 201", out["status"])
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(out["body"].(string)), &body); err != nil {
		t.Fatalf("body = %q", out["body"])
	}
	if body["ok"] != true || body["query"] != "2" {
		t.Fatalf("body = %+v", body)
	}
	if out["duration_ms"].(float64) < 0 {
		t.Fatalf("duration_ms = %v", out["duration_ms"])
	}
	if gotAuth != "" {
		t.Fatalf("unexpected auth header: %q", gotAuth)
	}
}

func TestHTTPRequestRejectsNonHTTP(t *testing.T) {
	req := mcp.CallToolRequest{Params: mcp.CallToolParams{Arguments: map[string]any{"url": "ftp://example.com"}}}
	res, err := handleHTTPRequest(context.Background(), req)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	if !res.IsError {
		t.Fatal("expected error result for non-http scheme")
	}
}

func TestAPITestPassAndFail(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("X-RateLimit", "60 remaining")
		_, _ = w.Write([]byte(`{"data":{"id":7},"items":[{"ok":true}]}`))
	}))
	defer srv.Close()

	pass := callTool(t, handleAPITest, map[string]any{
		"url":            srv.URL + "/v1/thing",
		"expect_status":  200,
		"expect_json":    map[string]any{"data.id": 7, "items.0.ok": true},
		"expect_contains": "items",
		"expect_header":  map[string]any{"X-Ratelimit": "60"},
	})
	if pass["passed"] != true {
		t.Fatalf("expected pass, got %+v", pass)
	}
	if pass["results"].([]any)[0].(map[string]any)["check"] != "status" {
		t.Fatalf("first check missing: %+v", pass["results"])
	}

	fail := callTool(t, handleAPITest, map[string]any{
		"url":          srv.URL + "/v1/thing",
		"expect_json":  map[string]any{"data.id": 8, "data.missing": "x"},
	})
	if fail["passed"] != false {
		t.Fatalf("expected fail, got %+v", fail)
	}
	if fail["body_excerpt"] == "" {
		t.Fatal("expected body excerpt on failure")
	}
}

func TestAuthProfiles(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer secret-tok" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	saved := callTool(t, handleSaveAuth, map[string]any{
		"name":        "local",
		"type":        "bearer",
		"token":       "secret-tok",
		"applies_to":  srv.URL,
	})
	if saved["saved"] != true {
		t.Fatalf("save_auth failed: %+v", saved)
	}

	out := callTool(t, handleAPITest, map[string]any{
		"url":          srv.URL + "/protected",
		"expect_status": 200,
	})
	if out["passed"] != true {
		t.Fatalf("auth not applied: %+v", out)
	}
	if applied, ok := out["applied_auth"].([]any); !ok || len(applied) != 1 || applied[0] != "local" {
		t.Fatalf("applied_auth = %v", out["applied_auth"])
	}

	listed := callTool(t, handleListAuth, map[string]any{})
	profiles := listed["profiles"].([]any)
	if len(profiles) != 1 {
		t.Fatalf("profiles = %+v", profiles)
	}
	if profiles[0].(map[string]any)["token"] != "secr…(redacted)" {
		t.Fatalf("token not redacted: %+v", profiles[0])
	}

	deleted := callTool(t, handleDeleteAuth, map[string]any{"name": "local"})
	if deleted["deleted"] != true {
		t.Fatalf("delete failed: %+v", deleted)
	}
	if after := callTool(t, handleListAuth, map[string]any{}); after["count"].(float64) != 0 {
		t.Fatalf("expected 0 profiles, got %+v", after)
	}
}

const testSpec = `openapi: 3.0.0
info:
  title: Demo API
  version: 1.0.0
servers:
  - url: https://api.demo.com
paths:
  /users/{id}:
    get:
      summary: Get user
      parameters:
        - name: id
          in: path
          required: true
          schema:
            type: string
  /users:
    post:
      summary: Create user
`

func TestOpenAPIEndpoints(t *testing.T) {
	path := filepath.Join(t.TempDir(), "spec.yaml")
	if err := os.WriteFile(path, []byte(testSpec), 0o600); err != nil {
		t.Fatal(err)
	}

	out := callTool(t, handleOpenAPIEndpoints, map[string]any{"source": path})
	if out["title"] != "Demo API" || out["version"] != "1.0.0" {
		t.Fatalf("meta = %+v", out)
	}
	endpoints := out["endpoints"].([]any)
	if len(endpoints) != 2 {
		t.Fatalf("endpoints = %+v", endpoints)
	}

	// Paths sort as /users before /users/{id}; check the GET operation via filter.
	getUser := callTool(t, handleOpenAPIEndpoints, map[string]any{"source": path, "method_filter": "get"})
	first := getUser["endpoints"].([]any)[0].(map[string]any)
	if first["method"] != "GET" || first["path"] != "/users/{id}" {
		t.Fatalf("first endpoint = %+v", first)
	}
	if first["full_url"] != "https://api.demo.com/users/{id}" {
		t.Fatalf("full_url = %v", first["full_url"])
	}
	params := first["parameters"].([]any)
	if len(params) != 1 || params[0] != "id (path, required)" {
		t.Fatalf("parameters = %+v", params)
	}

	filtered := callTool(t, handleOpenAPIEndpoints, map[string]any{
		"source":        path,
		"method_filter": "post",
	})
	feps := filtered["endpoints"].([]any)
	if len(feps) != 1 || feps[0].(map[string]any)["method"] != "POST" {
		t.Fatalf("filtered endpoints = %+v", feps)
	}
}
