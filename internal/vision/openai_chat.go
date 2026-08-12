package vision

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type openAIChatClient struct {
	client
}

func (c *openAIChatClient) Analyze(ctx context.Context, image []byte, prompt string) (string, error) {
	payload := map[string]any{
		"model": c.model,
		"messages": []any{
			map[string]any{
				"role":    "user",
				"content": chatContent(prompt, image, c.imageData),
			},
		},
	}
	c.tokenField(payload)
	responseBody, err := c.request(ctx, payload, c.authHeaders())
	if err != nil {
		return "", err
	}

	var response struct {
		Choices []struct {
			Message struct {
				Content json.RawMessage `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return "", fmt.Errorf("decode OpenAI Chat Completions response: %w", err)
	}
	if len(response.Choices) == 0 {
		return "", fmt.Errorf("OpenAI Chat Completions response contains no choices")
	}
	text := contentText(response.Choices[0].Message.Content)
	if text == "" {
		return "", fmt.Errorf("OpenAI Chat Completions response contains no text")
	}
	return strings.TrimSpace(text), nil
}

func chatContent(prompt string, image []byte, imageData func([]byte) string) []any {
	content := []any{map[string]any{"type": "text", "text": prompt}}
	if len(image) > 0 {
		content = append(content, map[string]any{"type": "image_url", "image_url": map[string]any{"url": imageData(image)}})
	}
	return content
}
