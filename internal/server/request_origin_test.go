package server

import (
	"crypto/tls"
	"net/http"
	"testing"
)

func TestRequestOrigin(t *testing.T) {
	tests := []struct {
		name   string
		host   string
		proto  string
		tls    bool
		expect string
	}{
		{name: "plain host", host: "127.0.0.1:9595", expect: "http://127.0.0.1:9595"},
		{name: "named host", host: "panel.example:8443", expect: "http://panel.example:8443"},
		{name: "tls", host: "ogcode.example", tls: true, expect: "https://ogcode.example"},
		{name: "forwarded https", host: "ogcode.example", proto: "https", expect: "https://ogcode.example"},
		{name: "forwarded https overrides tls", host: "ogcode.example", proto: "HTTPS", tls: true, expect: "https://ogcode.example"},
		{name: "unknown proto ignored", host: "ogcode.example", proto: "ftp", expect: "http://ogcode.example"},
		{name: "ipv6 host", host: "[::1]:9595", expect: "http://[::1]:9595"},
		{name: "empty host", host: "", expect: ""},
		{name: "host with space rejected", host: "bad host", expect: ""},
		{name: "host injection rejected", host: "evil.example/x", expect: ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := &http.Request{Host: tt.host, Header: http.Header{}}
			if tt.proto != "" {
				r.Header.Set("X-Forwarded-Proto", tt.proto)
			}
			if tt.tls {
				r.TLS = &tls.ConnectionState{}
			}
			if got := requestOrigin(r); got != tt.expect {
				t.Errorf("requestOrigin() = %q, want %q", got, tt.expect)
			}
		})
	}
}
