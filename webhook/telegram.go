package webhook

import (
	"encoding/json"
	"fmt"
	"html"
	"net/url"
	"strconv"
	"strings"
)

func buildTelegramPayload(cfg Config, event Event) ([]byte, error) {
	chatID := telegramChatID(cfg.URL)
	if chatID == "" {
		return nil, fmt.Errorf("Telegram webhook URL has no chat_id query parameter")
	}
	return json.Marshal(map[string]string{
		"chat_id": chatID, "text": telegramTextHTML(event), "parse_mode": "HTML",
	})
}

func telegramChatID(webhookURL string) string {
	u, err := url.Parse(webhookURL)
	if err != nil {
		return ""
	}
	return u.Query().Get("chat_id")
}

func telegramTextHTML(event Event) string {
	lines := []string{
		"<b>GFW " + html.EscapeString(strings.ToUpper(event.Event)) + "</b>",
	}
	if event.Host != "" {
		lines = append(lines, "Host: <code>"+html.EscapeString(event.Host)+"</code>")
	}
	lines = append(lines,
		"IP: <code>"+html.EscapeString(event.IP)+"</code>",
		"Protocol: <code>"+html.EscapeString(strings.ToUpper(event.Protocol))+"</code>",
	)
	if event.Port > 0 {
		lines = append(lines, "Port: <code>"+strconv.Itoa(event.Port)+"</code>")
	}
	if event.ControlOK != nil {
		status := "OK"
		if !*event.ControlOK {
			status = "ALSO DOWN"
		}
		lines = append(lines, "Control: <code>"+status+"</code>")
	} else if event.Event == "blocked" {
		lines = append(lines, "Control: <code>NOT CONFIGURED</code>")
	}
	if event.Reason != "" {
		lines = append(lines, "Reason: <code>"+html.EscapeString(event.Reason)+"</code>")
	}
	return strings.Join(lines, "\n")
}
