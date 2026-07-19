package webhook

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
)

type wecomResponse struct {
	ErrCode int    `json:"errcode"`
	ErrMsg  string `json:"errmsg"`
}

func buildWecomPayload(_ Config, event Event) ([]byte, error) {
	return json.Marshal(map[string]any{
		"msgtype": "text",
		"text":    map[string]string{"content": eventText(event)},
	})
}

func eventText(event Event) string {
	lines := []string{
		"GFW " + strings.ToUpper(event.Event),
		"IP: " + event.IP,
		"Protocol: " + strings.ToUpper(event.Protocol),
	}
	if event.Port > 0 {
		lines = append(lines, "Port: "+strconv.Itoa(event.Port))
	}
	if event.ControlOK != nil {
		status := "OK"
		if !*event.ControlOK {
			status = "ALSO DOWN"
		}
		lines = append(lines, "Control: "+status)
	} else if event.Event == "blocked" {
		lines = append(lines, "Control: NOT CONFIGURED")
	}
	if event.Reason != "" {
		lines = append(lines, "Reason: "+event.Reason)
	}
	return strings.Join(lines, "\n")
}

func checkWecomError(body []byte) error {
	var response wecomResponse
	if err := json.Unmarshal(body, &response); err != nil {
		return fmt.Errorf("invalid WeChat Work response: %w", err)
	}
	if response.ErrCode != 0 {
		return fmt.Errorf("WeChat Work API error: code=%d, msg=%s", response.ErrCode, response.ErrMsg)
	}
	return nil
}
