package checks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestNavigationCheck(t *testing.T) {
	body := strings.Repeat("x", 4096)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/ok":
			_, _ = w.Write([]byte(body))
		case "/tiny":
			_, _ = w.Write([]byte("hi"))
		case "/blocked":
			w.WriteHeader(http.StatusForbidden)
		}
	}))
	defer srv.Close()

	cases := []struct {
		name string
		url  string
		want bool
	}{
		{"real page passes", srv.URL + "/ok", true},
		{"tiny body fails", srv.URL + "/tiny", false},
		{"4xx fails", srv.URL + "/blocked", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := NavigationCheck{URL: tc.url}.Run(context.Background(), srv.Client())
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if res.Passed != tc.want {
				t.Fatalf("Passed = %v, want %v (detail %q)", res.Passed, tc.want, res.Detail)
			}
		})
	}
}

func TestNavigationCheckDialError(t *testing.T) {
	// An unreachable address must fail closed, not error out of Run.
	res, err := NavigationCheck{URL: "http://127.0.0.1:1/"}.Run(context.Background(), http.DefaultClient)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Passed {
		t.Fatalf("expected failure on dial error")
	}
}
