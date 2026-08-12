package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	tele "gopkg.in/telebot.v4"
)

type blockingPoller struct{}

func (blockingPoller) Poll(_ *tele.Bot, _ chan tele.Update, stop chan struct{}) {
	<-stop
}

func TestStopIsIdempotent(t *testing.T) {
	teleBot, err := tele.NewBot(tele.Settings{
		Token:   "TOKEN",
		Offline: true,
		Poller:  blockingPoller{},
	})
	if err != nil {
		t.Fatal(err)
	}

	bot := &Bot{bot: teleBot}
	started := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		close(started)
		bot.Start()
		close(finished)
	}()
	<-started

	stopped := make(chan struct{})
	go func() {
		bot.Stop()
		bot.Stop()
		close(stopped)
	}()

	select {
	case <-stopped:
	case <-time.After(time.Second):
		t.Fatal("Stop did not return for repeated calls")
	}
	select {
	case <-finished:
	case <-time.After(time.Second):
		t.Fatal("bot did not finish after Stop")
	}
}

func TestSendTextUsesRichMessage(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/botTOKEN/sendRichMessage" {
			t.Fatalf("path = %q, want /botTOKEN/sendRichMessage", r.URL.Path)
		}
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		var rich tele.InputRichMessage
		if err := json.Unmarshal([]byte(payload["rich_message"]), &rich); err != nil {
			t.Fatal(err)
		}
		if rich.Markdown != "# Heading\n\n- Item" {
			t.Fatalf("rich markdown = %q", rich.Markdown)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":1,"chat":{"id":123}}}`))
	}))
	defer server.Close()

	teleBot, err := tele.NewBot(tele.Settings{
		Token:   "TOKEN",
		URL:     server.URL,
		Offline: true,
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	bot := &Bot{bot: teleBot, chatID: "123"}
	if err := bot.SendText(context.Background(), "123", "# Heading\n\n- Item"); err != nil {
		t.Fatal(err)
	}
}

func TestSendReplyUsesPhotoMessageID(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/botTOKEN/sendRichMessage" {
			t.Fatalf("path = %q, want /botTOKEN/sendRichMessage", r.URL.Path)
		}
		var payload map[string]string
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if payload["reply_to_message_id"] != "77" {
			t.Fatalf("reply_to_message_id = %q, want 77", payload["reply_to_message_id"])
		}
		var rich tele.InputRichMessage
		if err := json.Unmarshal([]byte(payload["rich_message"]), &rich); err != nil {
			t.Fatal(err)
		}
		if rich.Markdown != "analysis" {
			t.Fatalf("rich markdown = %q, want analysis", rich.Markdown)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":2,"chat":{"id":123}}}`))
	}))
	defer server.Close()

	teleBot, err := tele.NewBot(tele.Settings{
		Token:   "TOKEN",
		URL:     server.URL,
		Offline: true,
		Client:  server.Client(),
	})
	if err != nil {
		t.Fatal(err)
	}
	bot := &Bot{bot: teleBot, chatID: "123"}
	if err := bot.SendReply(context.Background(), "123", "analysis", 77); err != nil {
		t.Fatal(err)
	}
}

func TestRegisterCommands(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/botTOKEN/setMyCommands" {
			t.Fatalf("path = %q, want /botTOKEN/setMyCommands", r.URL.Path)
		}
		var payload struct {
			Commands []tele.Command `json:"commands"`
		}
		if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
			t.Fatal(err)
		}
		if len(payload.Commands) != 7 || payload.Commands[0].Text != "screen" || payload.Commands[6].Text != "help" {
			t.Fatalf("commands = %+v", payload.Commands)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":true}`))
	}))
	defer server.Close()

	bot, err := tele.NewBot(tele.Settings{Token: "TOKEN", URL: server.URL, Offline: true, Client: server.Client()})
	if err != nil {
		t.Fatal(err)
	}
	if err := registerCommands(bot); err != nil {
		t.Fatal(err)
	}
}

func TestParseWorkflowPayload(t *testing.T) {
	if got := parseWorkflowPayload("workflow: screen-solution"); got != "screen-solution" {
		t.Fatalf("parseWorkflowPayload() = %q", got)
	}
	if got := parseWorkflowPayload("WORKFLOW=review"); got != "review" {
		t.Fatalf("parseWorkflowPayload() = %q", got)
	}
	if got := parseWorkflowPayload("codex"); got != "" {
		t.Fatalf("profile payload was parsed as workflow: %q", got)
	}
}

func TestAuthorizedChatRequiresAllowlistForGroups(t *testing.T) {
	group := &tele.Chat{ID: -100123}
	member := &tele.User{ID: 456}
	if authorizedChat("-100123", group, member, nil) {
		t.Fatal("group member was authorized without an allowlist")
	}
	if !authorizedChat("-100123", group, member, map[int64]struct{}{456: {}}) {
		t.Fatal("allowlisted group member was rejected")
	}
	privateChat := &tele.Chat{ID: 456}
	if !authorizedChat("456", privateChat, member, nil) {
		t.Fatal("private chat was rejected without an allowlist")
	}
}
