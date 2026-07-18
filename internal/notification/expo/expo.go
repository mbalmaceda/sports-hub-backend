package expo

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"

	"github.com/mbalmaceda/sports-hub-backend/internal/notification"
)

const pushURL = "https://exp.host/--/api/v2/push/send"

type Client struct {
	http *http.Client
}

func New() *Client {
	return &Client{http: &http.Client{}}
}

type pushMessage struct {
	To    string            `json:"to"`
	Title string            `json:"title"`
	Body  string            `json:"body"`
	Data  map[string]string `json:"data,omitempty"`
}

type pushResponse struct {
	Data []struct {
		Status  string `json:"status"`
		Message string `json:"message"`
	} `json:"data"`
}

func (c *Client) Send(ctx context.Context, msg notification.Message) error {
	return c.SendBatch(ctx, []notification.Message{msg})
}

func (c *Client) SendBatch(ctx context.Context, msgs []notification.Message) error {
	payload := make([]pushMessage, len(msgs))
	for i, m := range msgs {
		payload[i] = pushMessage{To: m.To, Title: m.Title, Body: m.Body, Data: m.Data}
	}

	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("expo: marshal: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, pushURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("expo: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("expo: send: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode >= 300 {
		return fmt.Errorf("expo: unexpected status %d", resp.StatusCode)
	}

	var result pushResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return fmt.Errorf("expo: decode response: %w", err)
	}

	for _, d := range result.Data {
		if d.Status == "error" {
			return fmt.Errorf("expo: push error: %s", d.Message)
		}
	}

	return nil
}
