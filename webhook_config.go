package watchdog

import (
	"fmt"
	"net/url"
	"strings"

	"gfw-watchdog/webhook"
)

func unquote(value string) string {
	value = strings.TrimSpace(value)
	if len(value) >= 2 && ((value[0] == '\'' && value[len(value)-1] == '\'') || (value[0] == '"' && value[len(value)-1] == '"')) {
		return value[1 : len(value)-1]
	}
	return value
}

func configFromFields(fields map[string]string) (webhook.Config, error) {
	for key := range fields {
		if key != "url" && key != "type" && key != "name" {
			return webhook.Config{}, fmt.Errorf("unknown field %q", key)
		}
	}
	cfg := webhook.Config{URL: fields["url"], Type: webhook.Type(fields["type"]), Name: fields["name"]}
	if cfg.URL == "" || cfg.Type == "" {
		return webhook.Config{}, fmt.Errorf("url and type are required")
	}
	parsedURL, err := url.ParseRequestURI(cfg.URL)
	if err != nil || parsedURL.Scheme == "" || parsedURL.Host == "" {
		return webhook.Config{}, fmt.Errorf("invalid URL %q", cfg.URL)
	}
	if cfg.Type != webhook.TypeTelegram && cfg.Type != webhook.TypeWecom {
		return webhook.Config{}, fmt.Errorf("invalid type %q, want telegram or wecom", cfg.Type)
	}
	if cfg.Type == webhook.TypeTelegram && parsedURL.Query().Get("chat_id") == "" {
		return webhook.Config{}, fmt.Errorf("Telegram URL requires chat_id query parameter")
	}
	if cfg.Name == "" {
		cfg.Name = string(cfg.Type)
	}
	return cfg, nil
}

func ParseWebhook(raw string) (webhook.Config, error) {
	fields := make(map[string]string)
	parts, err := splitWebhookFields(raw)
	if err != nil {
		return webhook.Config{}, err
	}
	for _, part := range parts {
		key, value, ok := strings.Cut(strings.TrimSpace(part), "=")
		if !ok || strings.TrimSpace(key) == "" {
			return webhook.Config{}, fmt.Errorf("invalid field %q", part)
		}
		fields[strings.TrimSpace(key)] = unquote(value)
	}
	return configFromFields(fields)
}

func splitWebhookFields(raw string) ([]string, error) {
	var fields []string
	start := 0
	var quote rune
	for index, char := range raw {
		switch {
		case quote != 0 && char == quote:
			quote = 0
		case quote == 0 && (char == '\'' || char == '"'):
			quote = char
		case quote == 0 && char == ',':
			fields = append(fields, raw[start:index])
			start = index + 1
		}
	}
	if quote != 0 {
		return nil, fmt.Errorf("unterminated quote")
	}
	fields = append(fields, raw[start:])
	return fields, nil
}

func ParseWebhooksEnv(raw string) ([]webhook.Config, error) {
	if strings.TrimSpace(raw) == "" {
		return nil, nil
	}
	lines := strings.Split(raw, "\n")
	yamlStyle := false
	for _, line := range lines {
		if strings.HasPrefix(strings.TrimSpace(line), "-") {
			yamlStyle = true
			break
		}
	}
	if !yamlStyle {
		var configs []webhook.Config
		for number, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" || strings.HasPrefix(line, "#") {
				continue
			}
			cfg, err := ParseWebhook(line)
			if err != nil {
				return nil, fmt.Errorf("line %d: %w", number+1, err)
			}
			configs = append(configs, cfg)
		}
		return configs, nil
	}
	var configs []webhook.Config
	var fields map[string]string
	flush := func() error {
		if fields == nil {
			return nil
		}
		cfg, err := configFromFields(fields)
		if err != nil {
			return err
		}
		configs = append(configs, cfg)
		return nil
	}
	for number, sourceLine := range lines {
		line := strings.TrimSpace(sourceLine)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		if strings.HasPrefix(line, "-") {
			if err := flush(); err != nil {
				return nil, fmt.Errorf("entry ending before line %d: %w", number+1, err)
			}
			fields = make(map[string]string)
			line = strings.TrimSpace(strings.TrimPrefix(line, "-"))
			if line == "" {
				continue
			}
		}
		if fields == nil {
			return nil, fmt.Errorf("line %d: expected '-' to start an entry", number+1)
		}
		key, value, ok := strings.Cut(line, ":")
		if !ok || strings.TrimSpace(key) == "" {
			return nil, fmt.Errorf("line %d: invalid YAML-style field", number+1)
		}
		fields[strings.TrimSpace(key)] = unquote(value)
	}
	if err := flush(); err != nil {
		return nil, fmt.Errorf("last entry: %w", err)
	}
	return configs, nil
}
