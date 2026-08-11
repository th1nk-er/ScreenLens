package vision

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/th1nk-er/ScreenLens/internal/config"
)

func TestProtocolAdapters(t *testing.T) {
	tests := []struct {
		name       string
		protocol   string
		want       string
		assertBody func(t *testing.T, body map[string]any)
	}{
		{
			name:     "OpenAI Chat Completions",
			protocol: config.ProtocolOpenAIChat,
			want:     "chat result",
			assertBody: func(t *testing.T, body map[string]any) {
				if body["max_completion_tokens"] != float64(128) {
					t.Fatalf("max_completion_tokens = %v", body["max_completion_tokens"])
				}
				if body["model"] != "vision-model" {
					t.Fatalf("model = %v", body["model"])
				}
				messages := body["messages"].([]any)
				content := messages[0].(map[string]any)["content"].([]any)
				if content[1].(map[string]any)["type"] != "image_url" {
					t.Fatalf("image content = %v", content[1])
				}
			},
		},
		{
			name:     "OpenAI Responses",
			protocol: config.ProtocolOpenAIResponses,
			want:     "responses result",
			assertBody: func(t *testing.T, body map[string]any) {
				if body["max_output_tokens"] != float64(128) {
					t.Fatalf("max_output_tokens = %v", body["max_output_tokens"])
				}
				input := body["input"].([]any)
				content := input[0].(map[string]any)["content"].([]any)
				if content[1].(map[string]any)["type"] != "input_image" {
					t.Fatalf("image content = %v", content[1])
				}
			},
		},
		{
			name:     "Anthropic Messages",
			protocol: config.ProtocolAnthropicMessages,
			want:     "anthropic result",
			assertBody: func(t *testing.T, body map[string]any) {
				messages := body["messages"].([]any)
				content := messages[0].(map[string]any)["content"].([]any)
				source := content[0].(map[string]any)["source"].(map[string]any)
				if source["data"] != base64.StdEncoding.EncodeToString([]byte("screen")) {
					t.Fatalf("image data = %v", source["data"])
				}
			},
		},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if got := r.Header.Get("Content-Type"); got != "application/json" {
					t.Errorf("Content-Type = %q", got)
				}
				if test.protocol == config.ProtocolAnthropicMessages {
					if got := r.Header.Get("x-api-key"); got != "secret" {
						t.Errorf("x-api-key = %q", got)
					}
					if got := r.Header.Get("anthropic-version"); got != "2023-06-01" {
						t.Errorf("anthropic-version = %q", got)
					}
				} else if got := r.Header.Get("Authorization"); got != "Bearer secret" {
					t.Errorf("Authorization = %q", got)
				}
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatal(err)
				}
				test.assertBody(t, body)
				w.Header().Set("Content-Type", "application/json")
				switch test.protocol {
				case config.ProtocolOpenAIChat:
					_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"chat result"}}]}`))
				case config.ProtocolOpenAIResponses:
					_, _ = w.Write([]byte(`{"output_text":"responses result"}`))
				case config.ProtocolAnthropicMessages:
					_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"anthropic result"}]}`))
				}
			}))
			defer server.Close()

			cfg := config.VisionConfig{
				Protocol:  test.protocol,
				Endpoint:  server.URL,
				Model:     "vision-model",
				APIKey:    "secret",
				MaxTokens: 128,
				Timeout:   "5s",
			}
			client, err := New(cfg, "image/jpeg")
			if err != nil {
				t.Fatal(err)
			}
			got, err := client.Analyze(context.Background(), []byte("screen"), "describe")
			if err != nil {
				t.Fatal(err)
			}
			if got != test.want {
				t.Fatalf("result = %q, want %q", got, test.want)
			}
		})
	}
}

func TestVisionProxyConfigurationIsValidated(t *testing.T) {
	cfg := config.VisionConfig{
		Protocol: config.ProtocolOpenAIChat,
		Endpoint: "http://127.0.0.1:1/v1/chat/completions",
		Model:    "vision-model",
		Proxy: config.ProxyConfig{
			Type: "socks5",
		},
	}
	if _, err := New(cfg, "image/jpeg"); err == nil {
		t.Fatal("New() error = nil, want invalid proxy configuration error")
	}
}

func TestAnthropicRejectsOversizedBase64Image(t *testing.T) {
	client := &anthropicClient{}
	_, err := client.Analyze(context.Background(), make([]byte, 7_500_001), "describe")
	if err == nil {
		t.Fatal("Analyze() error = nil, want base64 image size error")
	}
}
