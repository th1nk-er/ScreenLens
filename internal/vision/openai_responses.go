package vision

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

type openAIResponsesClient struct {
	client
}

func (c *openAIResponsesClient) Analyze(ctx context.Context, image []byte, prompt string) (string, error) {
	payload := map[string]any{
		"model": c.model,
		"input": []any{
			map[string]any{
				"role":    "user",
				"content": responsesContent(prompt, image, c.imageData),
			},
		},
	}
	c.tokenField(payload)
	responseBody, err := c.request(ctx, payload, c.authHeaders())
	if err != nil {
		return "", err
	}

	var response struct {
		OutputText string `json:"output_text"`
		Output     []struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"output"`
	}
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return "", fmt.Errorf("decode OpenAI Responses response: %w", err)
	}
	if strings.TrimSpace(response.OutputText) != "" {
		return strings.TrimSpace(response.OutputText), nil
	}
	var text strings.Builder
	for _, item := range response.Output {
		for _, part := range item.Content {
			if part.Text == "" {
				continue
			}
			if text.Len() > 0 {
				text.WriteString("\n")
			}
			text.WriteString(part.Text)
		}
	}
	if strings.TrimSpace(text.String()) == "" {
		return "", fmt.Errorf("OpenAI Responses response contains no text")
	}
	return strings.TrimSpace(text.String()), nil
}

func responsesContent(prompt string, image []byte, imageData func([]byte) string) []any {
	content := []any{map[string]any{"type": "input_text", "text": prompt}}
	if len(image) > 0 {
		content = append(content, map[string]any{"type": "input_image", "image_url": imageData(image), "detail": "auto"})
	}
	return content
}
