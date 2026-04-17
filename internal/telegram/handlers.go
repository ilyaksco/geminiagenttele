package telegram

import (
	"fmt"
	"gemini-agent/internal/database"
	"gemini-agent/internal/groq"
	"gemini-agent/internal/i18n"
	"log"
	"regexp"
	"strings"
)

type Handler struct {
	tg        *Client
	db        *database.DB
	llm       *groq.Client
	i18n      *i18n.I18n
	BotUser   *User
	IsManager bool
	OnNewBot  func(botID int64, token string)
}

func NewHandler(tg *Client, db *database.DB, llm *groq.Client, i18n *i18n.I18n, botUser *User, isManager bool, onNewBot func(botID int64, token string)) *Handler {
	return &Handler{
		tg:        tg,
		db:        db,
		llm:       llm,
		i18n:      i18n,
		BotUser:   botUser,
		IsManager: isManager,
		OnNewBot:  onNewBot,
	}
}

func (h *Handler) HandleUpdate(u Update) {
	if u.ManagedBot != nil {
		go h.handleManagedBotUpdate(u.ManagedBot)
	} else if u.Message != nil {
		go h.handleMessage(u.Message)
	} else if u.CallbackQuery != nil {
		go h.handleCallbackQuery(u.CallbackQuery)
	}
}

func (h *Handler) handleManagedBotUpdate(mb *ManagedBotUpdated) {
	if !h.IsManager {
		return
	}

	token, err := h.tg.GetManagedBotToken(mb.Bot.ID)
	if err != nil {
		log.Printf("Failed to get token for newly managed bot %d: %v\n", mb.Bot.ID, err)
		return
	}

	h.db.SaveManagedBot(mb.Bot.ID, mb.User.ID, mb.Bot.Username, token)
	log.Printf("Successfully registered managed bot @%s created by User %d\n", mb.Bot.Username, mb.User.ID)

	if h.OnNewBot != nil {
		h.OnNewBot(mb.Bot.ID, token)
	}
}

func (h *Handler) handleMessage(m *Message) {
	lang := h.db.GetUserLang(m.From.ID)

	if m.ForumTopicCreated != nil {
		if !h.IsManager {
			msg := h.i18n.Get(lang, "welcome")
			h.sendMessage(m.Chat.ID, m.MessageThreadID, 0, "Topic initialized. "+msg, true)
		}
		return
	}

	if m.Text == "" {
		return
	}

	if h.IsManager {
		if strings.HasPrefix(m.Text, "/link") {
			link := fmt.Sprintf("https://t.me/newbot/%s/my_new_bot", h.BotUser.Username)
			h.sendMessage(m.Chat.ID, m.MessageThreadID, m.MessageID, "Click here to create your own AI bot:\n"+link, false)
			return
		}

		if strings.HasPrefix(m.Text, "/setprompt") {
			parts := strings.SplitN(m.Text, " ", 3)
			if len(parts) < 3 {
				h.sendMessage(m.Chat.ID, m.MessageThreadID, m.MessageID, "Usage: /setprompt @bot_username <prompt_text>", false)
				return
			}

			targetUsername := strings.TrimSpace(strings.TrimPrefix(parts[1], "@"))
			promptText := strings.TrimSpace(parts[2])

			success := h.db.SetBotPromptByOwner(m.From.ID, targetUsername, promptText)
			if success {
				h.sendMessage(m.Chat.ID, m.MessageThreadID, m.MessageID, fmt.Sprintf("✅ Prompt updated successfully for @%s", targetUsername), false)
			} else {
				h.sendMessage(m.Chat.ID, m.MessageThreadID, m.MessageID, fmt.Sprintf("❌ Failed! Either @%s does not exist, or you are not the creator of that bot.", targetUsername), false)
			}
			return
		}

		if strings.HasPrefix(m.Text, "/start") || strings.HasPrefix(m.Text, "/help") {
			msg := "🤖 Welcome to AI Bot Factory!\n\nThis bot allows you to create your own personalized AI Assistant. Everyone can create one!\n\nCommands:\n/link - Create a new AI bot\n/setprompt @your_bot <text> - Set the AI persona\n/lang - Change language"
			h.sendMessage(m.Chat.ID, m.MessageThreadID, m.MessageID, msg, false)
			return
		}

		if strings.HasPrefix(m.Text, "/lang") {
			h.sendLangMenu(m.Chat.ID, m.MessageThreadID, lang)
			return
		}

		h.sendMessage(m.Chat.ID, m.MessageThreadID, m.MessageID, "⚠️ I am just the Bot Factory Manager without AI capabilities. Use /link to create your own AI bot, or /setprompt to manage your bot's persona.", false)
		return
	}

	isPrivate := m.Chat.Type == "private"
	isMentioned := false
	hasOtherMentions := false

	if h.BotUser != nil {
		botUsernameTag := "@" + h.BotUser.Username
		isMentioned = strings.Contains(m.Text, botUsernameTag)

		words := strings.Fields(m.Text)
		for _, w := range words {
			if strings.HasPrefix(w, "@") && !strings.Contains(w, botUsernameTag) {
				hasOtherMentions = true
				break
			}
		}
	}

	isReplyToMe := h.BotUser != nil && m.ReplyToMessage != nil && m.ReplyToMessage.From.ID == h.BotUser.ID

	shouldRespond := false
	if isPrivate {
		shouldRespond = true
	} else if isMentioned {
		shouldRespond = true
	} else if isReplyToMe && !hasOtherMentions {
		shouldRespond = true
	}

	if !shouldRespond {
		return
	}

	if strings.HasPrefix(m.Text, "/start") {
		msg := h.i18n.Get(lang, "welcome")
		h.sendMessage(m.Chat.ID, m.MessageThreadID, m.MessageID, msg, true)
		return
	}

	if strings.HasPrefix(m.Text, "/help") {
		msg := h.i18n.Get(lang, "help_message")
		h.sendMessage(m.Chat.ID, m.MessageThreadID, m.MessageID, msg, true)
		return
	}

	if strings.HasPrefix(m.Text, "/lang") {
		h.sendLangMenu(m.Chat.ID, m.MessageThreadID, lang)
		return
	}

	h.tg.SendChatAction(SendChatActionReq{
		ChatID:          m.Chat.ID,
		MessageThreadID: m.MessageThreadID,
		Action:          "typing",
	})

	promptText := m.Text
	if m.ReplyToMessage != nil && m.ReplyToMessage.Text != "" {
		repliedName := m.ReplyToMessage.From.FirstName
		if m.ReplyToMessage.From.IsBot {
			repliedName += " (Bot)"
		}
		promptText = fmt.Sprintf("[In reply to %s's message: \"%s\"]\n\nUser says: %s", repliedName, m.ReplyToMessage.Text, m.Text)
	}

	rawHistory := h.db.GetHistory(h.BotUser.ID, m.Chat.ID, m.MessageThreadID, 6)
	llmHistory := buildGroqHistory(rawHistory, promptText)

	h.db.SaveMessage(h.BotUser.ID, m.Chat.ID, m.MessageThreadID, "user", promptText)

	systemPrompt := ""
	if !h.IsManager {
		systemPrompt = h.db.GetBotPrompt(h.BotUser.ID)
	}

	replyText, err := h.llm.GenerateChat(systemPrompt, llmHistory)
	if err != nil {
		log.Printf("Error from Groq: %v\n", err)
		replyText = h.i18n.Get(lang, "error_occurred")
	} else {
		h.db.SaveMessage(h.BotUser.ID, m.Chat.ID, m.MessageThreadID, "assistant", replyText)
	}

	h.sendMessage(m.Chat.ID, m.MessageThreadID, m.MessageID, replyText, true)
}

func buildGroqHistory(rawHistory []database.ChatMessage, newText string) []groq.Message {
	var validHistory []groq.Message

	for _, msg := range rawHistory {
		role := msg.Role
		if role == "model" {
			role = "assistant"
		}
		validHistory = append(validHistory, groq.Message{
			Role:    role,
			Content: msg.Content,
		})
	}

	validHistory = append(validHistory, groq.Message{
		Role:    "user",
		Content: newText,
	})

	return validHistory
}

func formatToHTML(text string) string {
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")

	reCodeBlock := regexp.MustCompile("(?s)```(.*?)```")
	text = reCodeBlock.ReplaceAllString(text, "<pre><code>$1</code></pre>")

	reInlineCode := regexp.MustCompile("`([^`]+)`")
	text = reInlineCode.ReplaceAllString(text, "<code>$1</code>")

	reBold := regexp.MustCompile(`\*\*(.*?)\*\*`)
	text = reBold.ReplaceAllString(text, "<b>$1</b>")

	reItalic := regexp.MustCompile(`\*([^\*]+)\*`)
	text = reItalic.ReplaceAllString(text, "<i>$1</i>")

	return text
}

func (h *Handler) handleCallbackQuery(cq *CallbackQuery) {
	h.tg.AnswerCallbackQuery(cq.ID)

	parts := strings.Split(cq.Data, "_")
	if len(parts) == 2 && parts[0] == "lang" {
		newLang := parts[1]
		h.db.SetUserLang(cq.From.ID, newLang)

		if cq.Message != nil {
			successMsg := h.i18n.Get(newLang, "lang_changed")
			h.tg.EditMessageText(EditMessageTextReq{
				ChatID:    cq.Message.Chat.ID,
				MessageID: cq.Message.MessageID,
				Text:      successMsg,
			})
		}
	}
}

func (h *Handler) sendMessage(chatID int64, threadID int, replyToID int, text string, useHTML bool) {
	if useHTML {
		text = formatToHTML(text)
	}

	runes := []rune(text)
	chunkSize := 4000

	for i := 0; i < len(runes); i += chunkSize {
		end := i + chunkSize
		if end > len(runes) {
			end = len(runes)
		}
		chunk := string(runes[i:end])

		req := SendMessageReq{
			ChatID:           chatID,
			MessageThreadID:  threadID,
			ReplyToMessageID: replyToID,
			Text:             chunk,
		}

		if useHTML {
			req.ParseMode = "HTML"
		}

		err := h.tg.SendMessage(req)
		if err != nil && useHTML {
			log.Printf("Failed to send HTML chunk, falling back to plain text for chat %d: %v\n", chatID, err)
			req.ParseMode = ""
			rawRunes := []rune(h.stripHTML(chunk))
			req.Text = string(rawRunes)
			_ = h.tg.SendMessage(req)
		} else if err != nil {
			log.Printf("Failed to send message chunk to chat %d: %v\n", chatID, err)
		}

		replyToID = 0
	}
}

func (h *Handler) stripHTML(text string) string {
	text = strings.ReplaceAll(text, "<b>", "**")
	text = strings.ReplaceAll(text, "</b>", "**")
	text = strings.ReplaceAll(text, "<i>", "*")
	text = strings.ReplaceAll(text, "</i>", "*")
	text = strings.ReplaceAll(text, "<code>", "`")
	text = strings.ReplaceAll(text, "</code>", "`")
	text = strings.ReplaceAll(text, "<pre>", "```\n")
	text = strings.ReplaceAll(text, "</pre>", "\n```")
	text = strings.ReplaceAll(text, "&amp;", "&")
	text = strings.ReplaceAll(text, "&lt;", "<")
	text = strings.ReplaceAll(text, "&gt;", ">")
	return text
}

func (h *Handler) sendLangMenu(chatID int64, threadID int, lang string) {
	text := h.i18n.Get(lang, "choose_lang")
	btnEn := h.i18n.Get(lang, "btn_en")
	btnId := h.i18n.Get(lang, "btn_id")

	markup := InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: btnEn, CallbackData: "lang_en"},
				{Text: btnId, CallbackData: "lang_id"},
			},
		},
	}

	req := SendMessageReq{
		ChatID:          chatID,
		MessageThreadID: threadID,
		Text:            text,
		ReplyMarkup:     markup,
	}
	err := h.tg.SendMessage(req)
	if err != nil {
		log.Printf("Failed to send language menu to chat %d: %v\n", chatID, err)
	}
}