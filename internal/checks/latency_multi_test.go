package checks

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

// noContent serves 204 on /204 and 500 on /fail.
func newLatencyServer() *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/fail" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(http.StatusNoContent)
	}))
}

func TestLatencyCheckAllEndpointsMustPass(t *testing.T) {
	srv := newLatencyServer()
	defer srv.Close()

	// Both endpoints answer -> pass.
	res, err := LatencyCheck{URLs: []string{srv.URL + "/204", srv.URL + "/204"}}.Run(context.Background(), srv.Client())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !res.Passed {
		t.Fatalf("expected pass when both endpoints answer (detail %q)", res.Detail)
	}

	// One endpoint fails -> the whole gate fails.
	res, err = LatencyCheck{URLs: []string{srv.URL + "/204", srv.URL + "/fail"}}.Run(context.Background(), srv.Client())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if res.Passed {
		t.Fatalf("expected failure when one endpoint returns 5xx")
	}
}

func TestLatencyCheckTargetsPrecedence(t *testing.T) {
	if got := (LatencyCheck{URLs: []string{"a", "b"}, URL: "c"}).targets(); len(got) != 2 || got[0] != "a" {
		t.Fatalf("URLs should win: %v", got)
	}
	if got := (LatencyCheck{URL: "c"}).targets(); len(got) != 1 || got[0] != "c" {
		t.Fatalf("URL should be used when URLs empty: %v", got)
	}
	if got := (LatencyCheck{}).targets(); len(got) != len(DefaultLatencyURLs) {
		t.Fatalf("defaults should be used when both empty: %v", got)
	}
}
