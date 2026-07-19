package webhook

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
)

func TestTelegramPayloadEscapesHTML(t *testing.T) {
	body, err := buildTelegramPayload(Config{URL: "https://example.com/send?chat_id=42"}, Event{Event: "blocked", Host: "example.com", IP: "1.2.3.4", Protocol: "tcp", Reason: "a < b"})
	if err != nil {
		t.Fatal(err)
	}
	var payload map[string]string
	if err := json.Unmarshal(body, &payload); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(payload["text"], "a &lt; b") || !strings.Contains(payload["text"], "Host: <code>example.com</code>") || payload["chat_id"] != "42" || payload["parse_mode"] != "HTML" {
		t.Fatalf("unexpected payload: %#v", payload)
	}
}

func TestPostRetries(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.Copy(io.Discard, r.Body)
		if calls.Add(1) < 2 {
			http.Error(w, "retry", http.StatusServiceUnavailable)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		io.WriteString(w, `{"errcode":0}`)
	}))
	defer server.Close()
	if err := Post(context.Background(), server.Client(), Config{URL: server.URL, Type: TypeWecom}, Event{}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 {
		t.Fatalf("got %d calls", calls.Load())
	}
}
