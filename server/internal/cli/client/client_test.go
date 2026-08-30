package client

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestDecodeAPIError_Envelope(t *testing.T) {
	body := []byte(`{"error":{"code":"NOT_FOUND","message":"scope not found","details":{"field":"scope_id"},"trace_id":"req_abc"}}`)
	err := decodeAPIError(http.StatusNotFound, body)
	if err.Code != "NOT_FOUND" {
		t.Errorf("Code = %q, want NOT_FOUND", err.Code)
	}
	if err.Message != "scope not found" {
		t.Errorf("Message = %q", err.Message)
	}
	if err.TraceID != "req_abc" {
		t.Errorf("TraceID = %q", err.TraceID)
	}
	msg := err.Error()
	for _, want := range []string{"NOT_FOUND", "scope not found", "req_abc"} {
		if !strings.Contains(msg, want) {
			t.Errorf("Error() = %q, missing %q", msg, want)
		}
	}
}

func TestDecodeAPIError_NonEnvelopeBody(t *testing.T) {
	err := decodeAPIError(http.StatusBadGateway, []byte("upstream timeout"))
	if err.Message != "upstream timeout" {
		t.Errorf("Message = %q, want raw body", err.Message)
	}
	if err.Code != "" {
		t.Errorf("Code = %q, want empty for non-envelope body", err.Code)
	}
}

func TestDecodeAPIError_EmptyBody(t *testing.T) {
	err := decodeAPIError(http.StatusInternalServerError, nil)
	if err.Message != http.StatusText(http.StatusInternalServerError) {
		t.Errorf("Message = %q, want status text fallback", err.Message)
	}
}

func TestIsNotFound(t *testing.T) {
	if !IsNotFound(&APIError{StatusCode: http.StatusNotFound}) {
		t.Error("expected 404 APIError to be IsNotFound")
	}
	if IsNotFound(&APIError{StatusCode: http.StatusBadRequest}) {
		t.Error("expected 400 APIError to not be IsNotFound")
	}
	if IsNotFound(nil) {
		t.Error("expected non-APIError to not be IsNotFound")
	}
}

func TestClientGet_SuccessAndAuthHeader(t *testing.T) {
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]string{"id": "1", "name": "default"})
	}))
	defer srv.Close()

	c := New(srv.URL, "gdhcp_testtoken")
	var out map[string]string
	if err := c.get(context.Background(), "/api/v1/vrfs/1", nil, &out); err != nil {
		t.Fatalf("get: %v", err)
	}
	if out["name"] != "default" {
		t.Errorf("out = %v", out)
	}
	if gotAuth != "Bearer gdhcp_testtoken" {
		t.Errorf("Authorization header = %q", gotAuth)
	}
}

func TestClientGet_ErrorEnvelope(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusConflict)
		_ = json.NewEncoder(w).Encode(map[string]any{
			"error": map[string]any{"code": "CONFLICT", "message": "overlapping prefix"},
		})
	}))
	defer srv.Close()

	c := New(srv.URL, "")
	err := c.get(context.Background(), "/api/v1/prefixes", nil, nil)
	if err == nil {
		t.Fatal("expected error")
	}
	apiErr, ok := err.(*APIError)
	if !ok {
		t.Fatalf("expected *APIError, got %T: %v", err, err)
	}
	if apiErr.StatusCode != http.StatusConflict || apiErr.Code != "CONFLICT" {
		t.Errorf("apiErr = %+v", apiErr)
	}
}

func TestNormalizeServer(t *testing.T) {
	cases := map[string]string{
		"localhost:8080":            "http://localhost:8080",
		"http://localhost:8080/":    "http://localhost:8080",
		"https://dhcp.example.com":  "https://dhcp.example.com",
		"https://dhcp.example.com/": "https://dhcp.example.com",
	}
	for in, want := range cases {
		if got := normalizeServer(in); got != want {
			t.Errorf("normalizeServer(%q) = %q, want %q", in, got, want)
		}
	}
}
