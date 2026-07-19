package watchdog

import (
	"reflect"
	"testing"
	"time"
)

func TestTranslateShortArgs(t *testing.T) {
	in := []string{"-Hexample.com:80", "-i=1s-2s", "-F", "3s-10s", "-r", "3", "--", "-c", "x"}
	want := []string{"--host", "example.com:80", "--interval=1s-2s", "--failure-retry", "3s-10s", "--rise", "3", "--", "-c", "x"}
	if got := TranslateShortArgs(in); !reflect.DeepEqual(got, want) {
		t.Fatalf("got %#v, want %#v", got, want)
	}
}

func TestParseWebhooksEnv(t *testing.T) {
	raw := `- name: "primary"
  type: telegram
  url: "https://api.telegram.org/botTOKEN/sendMessage?chat_id=42"`
	configs, err := ParseWebhooksEnv(raw)
	if err != nil {
		t.Fatal(err)
	}
	if len(configs) != 1 || configs[0].Name != "primary" {
		t.Fatalf("unexpected configs: %#v", configs)
	}
}

func TestParseWebhookQuotedComma(t *testing.T) {
	cfg, err := ParseWebhook(`type=wecom,url="https://example.com/hook?labels=a,b",name=test`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.URL != "https://example.com/hook?labels=a,b" {
		t.Fatalf("unexpected URL: %s", cfg.URL)
	}
}

func TestParseConfigValidation(t *testing.T) {
	if _, err := ParseConfig(nil, ""); err == nil {
		t.Fatal("expected missing --host to fail")
	}
	if _, err := ParseConfig([]string{"--host", "1.2.3.4:80", "--rise", "0"}, ""); err == nil {
		t.Fatal("expected zero rise to fail")
	}
	cfg, err := ParseConfig([]string{"--host", "1.2.3.4:80", "--interval", "1s-2s"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(cfg.Targets) != 1 || cfg.Interval.Min.String() != "1s" || cfg.Interval.Max.String() != "2s" {
		t.Fatalf("unexpected config: %#v", cfg)
	}
	if _, err := ParseConfig([]string{"--ip", "1.2.3.4:80"}, ""); err == nil {
		t.Fatal("expected removed --ip option to fail")
	}
}

func TestFailureRetryConfig(t *testing.T) {
	cfg, err := ParseConfig([]string{"--host", "1.2.3.4:80", "--failure-retry", "5s-15s"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FailureRetry.Min != 5*time.Second || cfg.FailureRetry.Max != 15*time.Second {
		t.Fatalf("unexpected failure retry: %#v", cfg.FailureRetry)
	}
}

func TestFailureRetryDefault(t *testing.T) {
	cfg, err := ParseConfig([]string{"--host", "1.2.3.4:80"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if cfg.FailureRetry.Min != 5*time.Second || cfg.FailureRetry.Max != 10*time.Second {
		t.Fatalf("unexpected default failure retry: %#v", cfg.FailureRetry)
	}
}
