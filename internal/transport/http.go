package transport

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"strings"
	"time"

	"github.com/th1nk-er/ScreenLens/internal/config"
	"golang.org/x/net/proxy"
)

// NewHTTPClient creates the single HTTP client used by an integration. The
// proxy is configured at the transport level so every request, including
// Telegram long polling, follows the same network path.
func NewHTTPClient(proxyConfig config.ProxyConfig, timeout time.Duration) (*http.Client, error) {
	if timeout <= 0 {
		timeout = 2 * time.Minute
	}

	base, ok := http.DefaultTransport.(*http.Transport)
	if !ok {
		return nil, fmt.Errorf("http.DefaultTransport is not *http.Transport")
	}
	transport := base.Clone()

	proxyType := strings.ToLower(strings.TrimSpace(proxyConfig.Type))
	if proxyType == "" || proxyType == "none" || proxyType == "direct" {
		return &http.Client{Transport: transport, Timeout: timeout}, nil
	}
	if proxyType != "socks5" && proxyType != "socks5h" {
		return nil, fmt.Errorf("unsupported proxy type %q", proxyConfig.Type)
	}
	if strings.TrimSpace(proxyConfig.Address) == "" {
		return nil, fmt.Errorf("proxy address is required for %s", proxyConfig.Type)
	}

	dialer, err := proxy.SOCKS5("tcp", proxyConfig.Address, nil, proxy.Direct)
	if err != nil {
		return nil, fmt.Errorf("configure SOCKS5 proxy: %w", err)
	}
	transport.Proxy = nil
	transport.DialContext = func(ctx context.Context, network, address string) (net.Conn, error) {
		if contextDialer, ok := dialer.(proxy.ContextDialer); ok {
			return contextDialer.DialContext(ctx, network, address)
		}
		return dialer.Dial(network, address)
	}
	return &http.Client{Transport: transport, Timeout: timeout}, nil
}
