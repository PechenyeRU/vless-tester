package notify

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestShoutrrrSendsToWebhook(t *testing.T) {
	got := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		got <- string(b)
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	// shoutrrr's generic service over plain HTTP: generic+http://host:port/path.
	n, err := NewShoutrrr([]string{"generic+" + srv.URL})
	if err != nil || n == nil {
		t.Fatalf("new: err=%v nil=%v", err, n == nil)
	}
	if err := n.Notify(context.Background(), "hello world ✅"); err != nil {
		t.Fatalf("notify: %v", err)
	}
	select {
	case body := <-got:
		if !strings.Contains(body, "hello world") {
			t.Fatalf("webhook body missing message: %q", body)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("webhook was not called")
	}
}

func TestNewShoutrrrEmptyIsNil(t *testing.T) {
	n, err := NewShoutrrr([]string{"  ", ""})
	if err != nil || n != nil {
		t.Fatalf("empty URLs should yield a nil notifier, got n=%v err=%v", n, err)
	}
}

func TestCycleMessage(t *testing.T) {
	msg := CycleMessage("@WhiteDNS", 3, map[string]int{"FR": 2, "US": 1})
	if !strings.Contains(msg, "@WhiteDNS — 3 working servers") {
		t.Fatalf("header missing: %q", msg)
	}
	// Most-first ordering: FR (2) before US (1).
	if !strings.Contains(msg, "FR 2") || !strings.Contains(msg, "US 1") {
		t.Fatalf("per-country missing: %q", msg)
	}
	if strings.Index(msg, "FR 2") > strings.Index(msg, "US 1") {
		t.Fatalf("expected FR before US: %q", msg)
	}
}

func TestCycleMessageNoCountries(t *testing.T) {
	msg := CycleMessage("@WhiteDNS", 0, nil)
	if strings.Contains(msg, "\n") {
		t.Fatalf("empty breakdown should be one line: %q", msg)
	}
}
