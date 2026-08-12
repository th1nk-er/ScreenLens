package vision

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
)

const (
	anthropicMaxBase64ImageBytes = 10_000_000
	anthropicVersionHeader       = "anthropic-version"
	anthropicAPIVersion          = "2023-06-01"
)

type anthropicClient struct {
	client
}

func (c *anthropicClient) Analyze(ctx context.Context, image []byte, prompt string) (string, error) {
	if encodedSize := base64.StdEncoding.EncodedLen(len(image)); encodedSize > anthropicMaxBase64ImageBytes {
		return "", fmt.Errorf("Anthropic image exceeds the 10 MB base64 limit (%d bytes)", anthropicMaxBase64ImageBytes)
	}
	content := []any{map[string]any{"type": "text", "text": prompt}}
	if len(image) > 0 {
		content = []any{map[string]any{
			"type": "image",
			"source": map[string]any{
				"type":       "base64",
				"media_type": c.imageMIME,
				"data":       base64.StdEncoding.EncodeToString(image),
			},
		}, map[string]any{"type": "text", "text": prompt}}
	}
	payload := map[string]any{
		"model": c.model,
		"messages": []any{
			map[string]any{
				"role":    "user",
				"content": content,
			},
		},
	}
	c.tokenField(payload)
	headers := c.authHeaders()
	if !hasHeader(c.headers, anthropicVersionHeader) {
		headers[anthropicVersionHeader] = anthropicAPIVersion
	}
	responseBody, err := c.request(ctx, payload, headers)
	if err != nil {
		return "", err
	}

	var response struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return "", fmt.Errorf("decode Anthropic Messages response: %w", err)
	}
	var text strings.Builder
	for _, part := range response.Content {
		if part.Text == "" {
			continue
		}
		if text.Len() > 0 {
			text.WriteString("\n")
		}
		text.WriteString(part.Text)
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", fmt.Errorf("Anthropic Messages response contains no text")
	}
	return strings.TrimSpace(text.String()), nil
}
