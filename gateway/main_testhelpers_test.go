package main

import (
	"net/http/cookiejar"
	"net/url"
	"testing"
)

func newJar() (*cookiejar.Jar, error) {
	return cookiejar.New(nil)
}

func parseURL(t *testing.T, raw string) *url.URL {
	t.Helper()
	// websocket URLs (ws:// / wss://) share cookie scope with http:// / https://.
	httpURL := raw
	if len(httpURL) > 5 && httpURL[:5] == "ws://" {
		httpURL = "http://" + httpURL[5:]
	} else if len(httpURL) > 6 && httpURL[:6] == "wss://" {
		httpURL = "https://" + httpURL[6:]
	}
	u, err := url.Parse(httpURL)
	if err != nil {
		t.Fatal(err)
	}
	return u
}
