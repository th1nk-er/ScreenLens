package telegram

import (
	"bytes"
	"context"
	"fmt"
	"log/slog"
	"strconv"
	"strings"
	"sync"
	"time"

	tele "gopkg.in/telebot.v4"

	"github.com/th1nk-er/ScreenLens/internal/config"
	"github.com/th1nk-er/ScreenLens/internal/transport"
)

type Sender interface {
	SendText(ctx context.Context, target, text string) error
	SendPhoto(ctx context.Context, target string, image []byte, caption string) (int, error)
	SendReply(ctx context.Context, target, text string, replyTo int) error
}

type Handlers struct {
	Context  context.Context
	OnScreen func(context.Context, string) error
	OnReload func(context.Context) error
	Status   func() string
}

type Bot struct {
	bot      *tele.Bot
	chatID   string
	allowed  map[int64]struct{}
	handlers Handlers
	stopOnce sync.Once
}

type recipient string

func (r recipient) Recipient() string { return string(r) }

func New(cfg config.TelegramConfig, handlers Handlers) (*Bot, error) {
	httpClient, err := transport.NewHTTPClient(cfg.Proxy, cfg.RequestTimeout())
	if err != nil {
		return nil, err
	}
	pollTimeout := time.Duration(cfg.PollTimeout) * time.Second
	bot, err := tele.NewBot(tele.Settings{
		Token:       cfg.Token,
		Client:      httpClient,
		Poller:      &tele.LongPoller{Timeout: pollTimeout},
		Synchronous: true,
		OnError: func(err error, c tele.Context) {
			if c == nil || c.Chat() == nil {
				slog.Error("Telegram bot error", "error", err)
				return
			}
			slog.Error("Telegram bot error", "error", err, "chat_id", c.Chat().ID)
		},
	})
	if err != nil {
		return nil, fmt.Errorf("initialize Telegram bot: %w", err)
	}
	if err := registerCommands(bot); err != nil {
		return nil, fmt.Errorf("register Telegram commands: %w", err)
	}
	allowed := make(map[int64]struct{}, len(cfg.AllowedUserIDs))
	for _, userID := range cfg.AllowedUserIDs {
		allowed[userID] = struct{}{}
	}
	b := &Bot{bot: bot, chatID: cfg.ChatID, allowed: allowed, handlers: handlers}
	b.registerHandlers()
	return b, nil
}

func registerCommands(bot *tele.Bot) error {
	return bot.SetCommands([]tele.Command{
		{Text: "screen", Description: "Capture and analyze the current screen"},
		{Text: "reload", Description: "Reload capture and vision configuration"},
		{Text: "status", Description: "Show ScreenLens runtime status"},
		{Text: "help", Description: "Show available commands"},
	})
}

func (b *Bot) Start() {
	b.bot.Start()
}

func (b *Bot) Stop() {
	b.stopOnce.Do(func() {
		b.bot.Stop()
	})
}

func (b *Bot) SendText(ctx context.Context, target, text string) error {
	return b.sendRich(ctx, target, text, 0)
}

func (b *Bot) SendReply(ctx context.Context, target, text string, replyTo int) error {
	return b.sendRich(ctx, target, text, replyTo)
}

func (b *Bot) sendRich(ctx context.Context, target, text string, replyTo int) error {
	if err := contextErr(ctx); err != nil {
		return err
	}
	target = b.defaultTarget(target)
	if target == "" {
		return fmt.Errorf("Telegram target is empty")
	}
	// RichMessage carries extended Markdown in its own field, so it avoids
	// Telegram's legacy parse_mode escaping and the 4096-character text split.
	// The Bot API renders the result as one rich message.
	options := &tele.SendOptions{}
	if replyTo > 0 {
		options.ReplyTo = &tele.Message{ID: replyTo}
	}
	_, err := b.bot.Send(recipient(target), &tele.InputRichMessage{Markdown: text}, options)
	if err != nil {
		return fmt.Errorf("send Telegram rich message: %w", err)
	}
	return nil
}

func (b *Bot) SendPhoto(ctx context.Context, target string, image []byte, caption string) (int, error) {
	if err := contextErr(ctx); err != nil {
		return 0, err
	}
	target = b.defaultTarget(target)
	if target == "" {
		return 0, fmt.Errorf("Telegram target is empty")
	}
	photo := &tele.Photo{File: tele.FromReader(bytes.NewReader(image)), Caption: caption}
	message, err := b.bot.Send(recipient(target), photo, &tele.SendOptions{})
	if err != nil {
		return 0, fmt.Errorf("send Telegram photo: %w", err)
	}
	if message == nil || message.ID == 0 {
		return 0, fmt.Errorf("send Telegram photo: response has no message ID")
	}
	return message.ID, nil
}

func (b *Bot) registerHandlers() {
	b.bot.Handle("/screen", func(c tele.Context) error {
		if !b.authorized(c) {
			return nil
		}
		target := c.Chat().Recipient()
		if err := sendRich(c, "Capturing and analyzing the current screen..."); err != nil {
			return err
		}
		if b.handlers.OnScreen == nil {
			return sendRich(c, "Screen capture is not configured.")
		}
		if err := b.handlers.OnScreen(b.handlerContext(), target); err != nil {
			return sendRich(c, "Unable to queue capture: "+err.Error())
		}
		return nil
	})

	b.bot.Handle("/reload", func(c tele.Context) error {
		if !b.authorized(c) {
			return nil
		}
		if b.handlers.OnReload == nil {
			return sendRich(c, "Reload is not configured.")
		}
		if err := b.handlers.OnReload(b.handlerContext()); err != nil {
			return sendRich(c, "Reload failed: "+err.Error())
		}
		return sendRich(c, "Configuration reloaded.")
	})

	b.bot.Handle("/status", func(c tele.Context) error {
		if !b.authorized(c) {
			return nil
		}
		status := "running"
		if b.handlers.Status != nil {
			status = b.handlers.Status()
		}
		return sendRich(c, status)
	})

	b.bot.Handle("/help", func(c tele.Context) error {
		if !b.authorized(c) {
			return nil
		}
		return sendRichHTML(c, `<h1>ScreenLens commands</h1><ul><li><code>/screen</code> — capture and analyze the current screen</li><li><code>/reload</code> — reload capture and vision configuration</li><li><code>/status</code> — show runtime status</li><li><code>/help</code> — show this help</li></ul>`)
	})
}

func sendRich(c tele.Context, text string) error {
	return c.Send(&tele.InputRichMessage{Markdown: text}, &tele.SendOptions{})
}

func sendRichHTML(c tele.Context, html string) error {
	return c.Send(&tele.InputRichMessage{HTML: html}, &tele.SendOptions{})
}

func (b *Bot) authorized(c tele.Context) bool {
	if c == nil {
		return false
	}
	return authorizedChat(b.chatID, c.Chat(), c.Sender(), b.allowed)
}

func authorizedChat(configuredChatID string, chat *tele.Chat, sender *tele.User, allowed map[int64]struct{}) bool {
	if chat == nil || strconv.FormatInt(chat.ID, 10) != configuredChatID || sender == nil {
		return false
	}
	if len(allowed) > 0 {
		_, ok := allowed[sender.ID]
		return ok
	}
	// Without an explicit allowlist, only a private chat is safe. In a group,
	// chat_id alone identifies the room and would otherwise authorize every
	// member to control the local assistant.
	return chat.ID == sender.ID
}

func (b *Bot) handlerContext() context.Context {
	if b.handlers.Context != nil {
		return b.handlers.Context
	}
	return context.Background()
}

func (b *Bot) defaultTarget(target string) string {
	if strings.TrimSpace(target) == "" {
		return b.chatID
	}
	return strings.TrimSpace(target)
}

func contextErr(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	default:
		return nil
	}
}

var _ Sender = (*Bot)(nil)
