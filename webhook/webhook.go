package webhook

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

type Type string

const (
	TypeTelegram Type = "telegram"
	TypeWecom    Type = "wecom"
)

type Config struct {
	URL  string
	Type Type
	Name string
}

type Event struct {
	Version             int    `json:"version"`
	Event               string `json:"event"`
	Timestamp           string `json:"timestamp"`
	IP                  string `json:"ip"`
	Protocol            string `json:"protocol"`
	Port                int    `json:"port,omitempty"`
	Reason              string `json:"reason,omitempty"`
	ControlOK           *bool  `json:"control_ok,omitempty"`
	ConsecutiveFailures int    `json:"consecutive_failures,omitempty"`
}

type worker struct {
	webhook Config
	queue   chan Event
}

type Notifier struct {
	client  *http.Client
	workers []worker
	ctx     context.Context
	cancel  context.CancelFunc
	wg      sync.WaitGroup
	mu      sync.RWMutex
	closed  bool
}

func NewNotifier(configs []Config, client *http.Client) *Notifier {
	ctx, cancel := context.WithCancel(context.Background())
	n := &Notifier{client: client, ctx: ctx, cancel: cancel}
	for _, cfg := range configs {
		w := worker{webhook: cfg, queue: make(chan Event, 256)}
		n.workers = append(n.workers, w)
		n.wg.Add(1)
		go n.runWorker(w)
	}
	return n
}

func (n *Notifier) Publish(event Event) {
	n.mu.RLock()
	defer n.mu.RUnlock()
	if n.closed {
		return
	}
	for _, w := range n.workers {
		select {
		case w.queue <- event:
		default:
			log.Printf("webhook queue full, dropping %s event for %s (%s)", event.Event, event.IP, w.webhook.Name)
		}
	}
}

func (n *Notifier) runWorker(w worker) {
	defer n.wg.Done()
	for event := range w.queue {
		if err := Post(n.ctx, n.client, w.webhook, event); err != nil {
			log.Printf("notification to %s failed: %v", w.webhook.Name, err)
		}
	}
}

func (n *Notifier) Close(timeout time.Duration) bool {
	n.mu.Lock()
	if !n.closed {
		n.closed = true
		for _, w := range n.workers {
			close(w.queue)
		}
	}
	n.mu.Unlock()
	done := make(chan struct{})
	go func() {
		n.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		n.cancel()
		return true
	case <-time.After(timeout):
		n.cancel()
		<-done
		return false
	}
}

func Post(ctx context.Context, client *http.Client, cfg Config, event Event) error {
	body, err := buildPayload(cfg, event)
	if err != nil {
		return err
	}
	var lastErr error
	for attempt := 0; attempt < 4; attempt++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, cfg.URL, bytes.NewReader(body))
		if err != nil {
			return err
		}
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			lastErr = err
		} else {
			respBody, readErr := io.ReadAll(resp.Body)
			resp.Body.Close()
			if readErr != nil {
				lastErr = readErr
			} else if resp.StatusCode < 200 || resp.StatusCode >= 300 {
				lastErr = fmt.Errorf("HTTP status %s", resp.Status)
			} else if cfg.Type == TypeWecom {
				lastErr = checkWecomError(respBody)
				if lastErr == nil {
					return nil
				}
			} else {
				return nil
			}
		}
		if attempt == 3 {
			break
		}
		timer := time.NewTimer(time.Second << attempt)
		select {
		case <-timer.C:
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		}
	}
	return lastErr
}

func buildPayload(cfg Config, event Event) ([]byte, error) {
	switch cfg.Type {
	case TypeTelegram:
		return buildTelegramPayload(cfg, event)
	case TypeWecom:
		return buildWecomPayload(cfg, event)
	default:
		return nil, fmt.Errorf("unsupported webhook type %q", cfg.Type)
	}
}
