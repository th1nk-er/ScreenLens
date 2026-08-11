package vision

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/th1nk-er/ScreenLens/internal/config"
	"github.com/th1nk-er/ScreenLens/internal/transport"
)

// Vision is deliberately protocol-oriented. A vendor that implements one of
// the supported wire protocols only needs endpoint/model configuration; the
// workflow does not know or care which vendor is behind the endpoint.
type Vision interface {
	Analyze(ctx context.Context, image []byte, prompt string) (string, error)
}

type client struct {
	httpClient     *http.Client
	endpoint       string
	model          string
	apiKey         string
	apiKeyHeader   string
	apiKeyPrefix   string
	headers        map[string]string
	imageMIME      string
	maxTokens      int
	maxTokensField string
}

func New(cfg config.VisionConfig, imageMIME string) (Vision, error) {
	protocol := config.NormalizeProtocol(cfg.Protocol, cfg.Provider)
	if protocol == "" {
		protocol = config.ProtocolOpenAIChat
	}
	if cfg.Endpoint == "" {
		cfg.Endpoint = config.DefaultEndpoint(protocol)
	}
	if cfg.APIKeyHeader == "" {
		cfg.APIKeyHeader = config.DefaultAPIKeyHeader(protocol)
	}
	if cfg.APIKeyPrefix == "" && protocol != config.ProtocolAnthropicMessages {
		cfg.APIKeyPrefix = "Bearer"
	}
	if cfg.MaxTokensField == "" {
		cfg.MaxTokensField = config.DefaultMaxTokensField(protocol)
	}
	httpClient, err := transport.NewHTTPClient(cfg.Proxy, cfg.RequestTimeout())
	if err != nil {
		return nil, err
	}
	if imageMIME == "" {
		imageMIME = "image/jpeg"
	}
	base := client{
		httpClient:     httpClient,
		endpoint:       cfg.Endpoint,
		model:          cfg.Model,
		apiKey:         cfg.APIKey,
		apiKeyHeader:   cfg.APIKeyHeader,
		apiKeyPrefix:   cfg.APIKeyPrefix,
		headers:        cloneHeaders(cfg.Headers),
		imageMIME:      imageMIME,
		maxTokens:      cfg.MaxTokens,
		maxTokensField: cfg.MaxTokensField,
	}

	switch protocol {
	case config.ProtocolOpenAIChat:
		return &openAIChatClient{client: base}, nil
	case config.ProtocolOpenAIResponses:
		return &openAIResponsesClient{client: base}, nil
	case config.ProtocolAnthropicMessages:
		return &anthropicClient{client: base}, nil
	default:
		return nil, fmt.Errorf("unsupported vision protocol %q", cfg.Protocol)
	}
}

func (c *client) request(ctx context.Context, payload any, extraHeaders map[string]string) ([]byte, error) {
	body, err := json.Marshal(payload)
	if err != nil {
		return nil, fmt.Errorf("encode vision request: %w", err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("create vision request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	for key, value := range c.headers {
		req.Header.Set(key, value)
	}
	for key, value := range extraHeaders {
		req.Header.Set(key, value)
	}

	response, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("vision request: %w", err)
	}
	defer response.Body.Close()
	responseBody, err := io.ReadAll(io.LimitReader(response.Body, 4<<20))
	if err != nil {
		return nil, fmt.Errorf("read vision response: %w", err)
	}
	if response.StatusCode < http.StatusOK || response.StatusCode >= http.StatusMultipleChoices {
		message := strings.TrimSpace(string(responseBody))
		if len(message) > 1000 {
			message = message[:1000] + "..."
		}
		return nil, fmt.Errorf("vision API returned %s: %s", response.Status, message)
	}
	return responseBody, nil
}

func (c client) authHeaders() map[string]string {
	headers := make(map[string]string, 2)
	if c.apiKey == "" {
		return headers
	}
	prefix := strings.TrimSpace(c.apiKeyPrefix)
	value := c.apiKey
	if prefix != "" {
		value = prefix + " " + value
	}
	if hasHeader(c.headers, c.apiKeyHeader) {
		return headers
	}
	headers[c.apiKeyHeader] = value
	return headers
}

func hasHeader(headers map[string]string, name string) bool {
	for key := range headers {
		if strings.EqualFold(key, name) {
			return true
		}
	}
	return false
}

func (c client) imageData(image []byte) string {
	return "data:" + c.imageMIME + ";base64," + base64.StdEncoding.EncodeToString(image)
}

func (c client) tokenField(target map[string]any) {
	if c.maxTokens <= 0 {
		return
	}
	field := c.maxTokensField
	if field == "" {
		field = "max_tokens"
	}
	target[field] = c.maxTokens
}

func cloneHeaders(source map[string]string) map[string]string {
	if len(source) == 0 {
		return nil
	}
	target := make(map[string]string, len(source))
	for key, value := range source {
		target[key] = value
	}
	return target
}

func contentText(raw json.RawMessage) string {
	var value string
	if json.Unmarshal(raw, &value) == nil {
		return strings.TrimSpace(value)
	}
	var parts []struct {
		Type string `json:"type"`
		Text string `json:"text"`
	}
	if json.Unmarshal(raw, &parts) == nil {
		var text strings.Builder
		for _, part := range parts {
			if part.Text != "" {
				if text.Len() > 0 {
					text.WriteString("\n")
				}
				text.WriteString(part.Text)
			}
		}
		return strings.TrimSpace(text.String())
	}
	return ""
}
