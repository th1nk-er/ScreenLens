package telegram

import (
	"bytes"
	"context"
	"errors"
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

const (
	commandScreen   = "screen"
	commandReload   = "reload"
	commandStatus   = "status"
	commandAgent    = "agent"
	commandWorkflow = "workflow"
	commandCancel   = "cancel"
	commandHelp     = "help"
	statusRunning   = "running"
)

var errTelegramTargetEmpty = errors.New("Telegram target is empty")

type Sender interface {
	SendText(ctx context.Context, target, text string) error
	SendPhoto(ctx context.Context, target string, image []byte, caption string) (int, error)
	SendReply(ctx context.Context, target, text string, replyTo int) error
}

type Handlers struct {
	Context          context.Context
	OnScreen         func(context.Context, string) error
	OnScreenProfile  func(context.Context, string, string) error
	OnScreenWorkflow func(context.Context, string, string) error
	OnReload         func(context.Context) error
	OnCancel         func(context.Context) bool
	Agents           func() string
	Workflows        func() string
	Status           func() string
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
		{Text: commandScreen, Description: "Capture and analyze the current screen"},
		{Text: commandReload, Description: "Reload capture and vision configuration"},
		{Text: commandStatus, Description: "Show ScreenLens runtime status"},
		{Text: commandAgent, Description: "List or select a local agent profile"},
		{Text: commandWorkflow, Description: "List configured analysis workflows"},
		{Text: commandCancel, Description: "Cancel the active capture"},
		{Text: commandHelp, Description: "Show available commands"},
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
		return errTelegramTargetEmpty
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
		return 0, errTelegramTargetEmpty
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
	b.bot.Handle("/"+commandScreen, func(c tele.Context) error {
		if !b.authorized(c) {
			return nil
		}
		target := c.Chat().Recipient()
		profile := ""
		workflowName := ""
		if c.Message() != nil {
			profile = strings.TrimSpace(c.Message().Payload)
			workflowName = parseWorkflowPayload(profile)
			if workflowName != "" {
				profile = ""
			}
		}
		message := "Capturing and analyzing the current screen..."
		if workflowName != "" {
			message = "Capturing and analyzing with workflow `" + workflowName + "`..."
		} else if profile != "" {
			message = "Capturing and analyzing with profile `" + profile + "`..."
		}
		if err := sendRich(c, message); err != nil {
			return err
		}
		if b.handlers.OnScreen == nil && b.handlers.OnScreenProfile == nil && b.handlers.OnScreenWorkflow == nil {
			return sendRich(c, "Screen capture is not configured.")
		}
		var err error
		if workflowName != "" && b.handlers.OnScreenWorkflow != nil {
			err = b.handlers.OnScreenWorkflow(b.handlerContext(), target, workflowName)
		} else if workflowName != "" {
			err = fmt.Errorf("workflow selection is not configured")
		} else if b.handlers.OnScreenProfile != nil {
			err = b.handlers.OnScreenProfile(b.handlerContext(), target, profile)
		} else {
			err = b.handlers.OnScreen(b.handlerContext(), target)
		}
		if err != nil {
			return sendRich(c, "Unable to queue capture: "+err.Error())
		}
		return nil
	})

	b.bot.Handle("/"+commandWorkflow, func(c tele.Context) error {
		if !b.authorized(c) {
			return nil
		}
		if b.handlers.Workflows == nil {
			return sendRich(c, "Analysis workflows are not configured.")
		}
		return sendRich(c, b.handlers.Workflows())
	})

	b.bot.Handle("/"+commandReload, func(c tele.Context) error {
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

	b.bot.Handle("/"+commandStatus, func(c tele.Context) error {
		if !b.authorized(c) {
			return nil
		}
		status := statusRunning
		if b.handlers.Status != nil {
			status = b.handlers.Status()
		}
		return sendRich(c, status)
	})

	b.bot.Handle("/"+commandAgent, func(c tele.Context) error {
		if !b.authorized(c) {
			return nil
		}
		if b.handlers.Agents == nil {
			return sendRich(c, "Local agent profiles are not configured.")
		}
		return sendRich(c, b.handlers.Agents())
	})

	b.bot.Handle("/"+commandCancel, func(c tele.Context) error {
		if !b.authorized(c) {
			return nil
		}
		if b.handlers.OnCancel == nil {
			return sendRich(c, "Cancellation is not configured.")
		}
		if !b.handlers.OnCancel(b.handlerContext()) {
			return sendRich(c, "No capture is currently running.")
		}
		return sendRich(c, "Capture cancellation requested.")
	})

	b.bot.Handle("/"+commandHelp, func(c tele.Context) error {
		if !b.authorized(c) {
			return nil
		}
		return sendRichHTML(c, `<h1>ScreenLens commands</h1><ul><li><code>/screen [profile]</code> - capture and analyze the current screen</li><li><code>/screen workflow:&lt;name&gt;</code> - run a configured workflow</li><li><code>/agent</code> - list local agent profiles</li><li><code>/workflow</code> - list configured workflows</li><li><code>/cancel</code> - cancel the active capture</li><li><code>/reload</code> - reload configuration</li><li><code>/status</code> - show runtime status</li><li><code>/help</code> - show this help</li></ul>`)
	})
}

func parseWorkflowPayload(payload string) string {
	payload = strings.TrimSpace(payload)
	for _, prefix := range []string{"workflow:", "workflow="} {
		if strings.HasPrefix(strings.ToLower(payload), prefix) {
			return strings.TrimSpace(payload[len(prefix):])
		}
	}
	return ""
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
