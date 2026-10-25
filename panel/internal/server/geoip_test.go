package server

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestCountryISProviderValidatesAndNormalizesResponse(t *testing.T) {
	server := httptest.NewTLSServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/2001:4860:4860::8888" {
			t.Fatalf("request path = %q", request.URL.Path)
		}
		writer.Header().Set("Content-Type", "application/json")
		_, _ = writer.Write([]byte(`{"ip":"2001:4860:4860::8888","country":"tw"}`))
	}))
	defer server.Close()
	provider := &countryISProvider{baseURL: server.URL, client: server.Client()}
	code, err := provider.CountryCode(context.Background(), "2001:4860:4860::8888")
	if err != nil || code != "TW" {
		t.Fatalf("CountryCode() = %q, %v, want TW", code, err)
	}
}

func TestCountryISProviderRejectsBadResponses(t *testing.T) {
	for _, testCase := range []struct {
		name   string
		status int
		body   string
	}{
		{name: "HTTP status", status: http.StatusTooManyRequests, body: `{}`},
		{name: "invalid JSON", status: http.StatusOK, body: `{`},
		{name: "missing code", status: http.StatusOK, body: `{"country":""}`},
		{name: "invalid code", status: http.StatusOK, body: `{"country":"DEU"}`},
		{name: "oversized body", status: http.StatusOK, body: strings.Repeat("x", geoIPMaxResponseSize+1)},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
				writer.WriteHeader(testCase.status)
				_, _ = writer.Write([]byte(testCase.body))
			}))
			defer server.Close()
			provider := &countryISProvider{baseURL: server.URL, client: server.Client()}
			if _, err := provider.CountryCode(context.Background(), "8.8.8.8"); err == nil {
				t.Fatal("CountryCode() accepted invalid response")
			}
		})
	}
}

func TestCountryISProviderHonorsContextCancellation(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, request *http.Request) {
		<-request.Context().Done()
	}))
	defer server.Close()
	provider := &countryISProvider{baseURL: server.URL, client: server.Client()}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, err := provider.CountryCode(ctx, "8.8.8.8")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("CountryCode() error = %v, want context deadline exceeded", err)
	}
}

func TestNormalizeCountryCode(t *testing.T) {
	for _, testCase := range []struct {
		input string
		want  string
		valid bool
	}{
		{input: "de", want: "DE", valid: true},
		{input: " TW ", want: "TW", valid: true},
		{input: "HK", want: "HK", valid: true},
		{input: "MO", want: "MO", valid: true},
		{input: "", valid: false},
		{input: "D", valid: false},
		{input: "DEU", valid: false},
		{input: "123", valid: false},
		{input: "ſg", valid: false},
	} {
		got, err := normalizeCountryCode(testCase.input)
		if testCase.valid && (err != nil || got != testCase.want) {
			t.Errorf("normalizeCountryCode(%q) = %q, %v", testCase.input, got, err)
		}
		if !testCase.valid && err == nil {
			t.Errorf("normalizeCountryCode(%q) accepted %q", testCase.input, got)
		}
	}
}
