package telegram

import (
	"fmt"
	"gemini-agent/internal/crypto"
	"gemini-agent/internal/database"
	"gemini-agent/internal/groq"
	"gemini-agent/internal/i18n"
	"log"
	"regexp"
	"strings"
	"sync"
)

type Handler struct {
	tg            *Client
	db            *database.DB
	llm           *groq.Client
	i18n          *i18n.I18n
	BotUser       *User
	EncryptionKey string
	IsManager     bool
	OnNewBot      func(botID int64, token string)
	userStates    map[int64]string
	stateMu       sync.RWMutex
}

func NewHandler(tg *Client, db *database.DB, llm *groq.Client, i18n *i18n.I18n, botUser *User, encryptionKey string, isManager bool, onNewBot func(botID int64, token string)) *Handler {
	return &Handler{
		tg:            tg,
		db:            db,
		llm:           llm,
		i18n:          i18n,
		BotUser:       botUser,
		EncryptionKey: encryptionKey,
		IsManager:     isManager,
		OnNewBot:      onNewBot,
		userStates:    make(map[int64]string),
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
			h.sendMsg(m.Chat.ID, m.MessageThreadID, 0, "Topic initialized. "+msg, true, nil)
		}
		return
	}

	if m.Text == "" {
		return
	}

	if h.IsManager {
		h.stateMu.RLock()
		state := h.userStates[m.From.ID]
		h.stateMu.RUnlock()

		if state == "awaiting_api_key" {
			rawKeys := strings.ReplaceAll(m.Text, " ", "")
			encrypted, err := crypto.Encrypt(rawKeys, h.EncryptionKey)
			if err != nil {
				h.sendMsg(m.Chat.ID, m.MessageThreadID, m.MessageID, h.i18n.Get(lang, "error_occurred"), true, nil)
			} else {
				h.db.SetUserAPIKeys(m.From.ID, encrypted)
				h.sendMsg(m.Chat.ID, m.MessageThreadID, m.MessageID, h.i18n.Get(lang, "api_saved"), true, nil)
			}

			h.stateMu.Lock()
			delete(h.userStates, m.From.ID)
			h.stateMu.Unlock()
			return
		}

		if strings.HasPrefix(m.Text, "/start") {
			msg := h.i18n.Get(lang, "welcome")
			h.sendMainMenu(m.Chat.ID, m.MessageThreadID, lang, msg)
			return
		}

		if strings.HasPrefix(m.Text, "/link") {
			h.handleCreateBotFlow(m.Chat.ID, m.MessageThreadID, lang)
			return
		}

		if strings.HasPrefix(m.Text, "/mybots") {
			h.sendMyBots(m.From.ID, m.Chat.ID, m.MessageThreadID, lang)
			return
		}

		if strings.HasPrefix(m.Text, "/setapi") {
			h.handleSetApiFlow(m.From.ID, m.Chat.ID, m.MessageThreadID, lang)
			return
		}

		if strings.HasPrefix(m.Text, "/setprompt") {
			parts := strings.SplitN(m.Text, " ", 3)
			if len(parts) < 3 {
				h.sendMsg(m.Chat.ID, m.MessageThreadID, m.MessageID, "Usage: `/setprompt @bot_username <prompt_text>`", true, nil)
				return
			}

			targetUsername := strings.TrimSpace(strings.TrimPrefix(parts[1], "@"))
			promptText := strings.TrimSpace(parts[2])

			success := h.db.SetBotPromptByOwner(m.From.ID, targetUsername, promptText)
			if success {
				h.sendMsg(m.Chat.ID, m.MessageThreadID, m.MessageID, fmt.Sprintf("✅ Prompt updated successfully for @%s", targetUsername), true, nil)
			} else {
				h.sendMsg(m.Chat.ID, m.MessageThreadID, m.MessageID, fmt.Sprintf("❌ Failed! Either @%s does not exist, or you are not the creator of that bot.", targetUsername), true, nil)
			}
			return
		}

		if strings.HasPrefix(m.Text, "/help") {
			msg := h.i18n.Get(lang, "help_message")
			h.sendMsg(m.Chat.ID, m.MessageThreadID, m.MessageID, msg, true, nil)
			return
		}

		if strings.HasPrefix(m.Text, "/lang") {
			h.sendLangMenu(m.Chat.ID, m.MessageThreadID, lang)
			return
		}

		h.sendMsg(m.Chat.ID, m.MessageThreadID, m.MessageID, "⚠️ Use /start to open the menu.", true, nil)
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
		h.sendMsg(m.Chat.ID, m.MessageThreadID, m.MessageID, msg, true, nil)
		return
	}

	h.tg.SendChatAction(SendChatActionReq{
		ChatID:          m.Chat.ID,
		MessageThreadID: m.MessageThreadID,
		Action:          "typing",
	})

	ownerID := h.db.GetBotOwner(h.BotUser.ID)
	encryptedKeys := h.db.GetUserAPIKeys(ownerID)
	decryptedKeys := ""
	if encryptedKeys != "" {
		decryptedKeys, _ = crypto.Decrypt(encryptedKeys, h.EncryptionKey)
	}

	apiKeys := strings.Split(decryptedKeys, ",")
	validKeys := []string{}
	for _, k := range apiKeys {
		if k != "" {
			validKeys = append(validKeys, k)
		}
	}

	if len(validKeys) == 0 {
		h.sendMsg(m.Chat.ID, m.MessageThreadID, m.MessageID, h.i18n.Get(lang, "missing_api_key"), true, nil)
		return
	}

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

	systemPrompt := h.db.GetBotPrompt(h.BotUser.ID)

	replyText, err := h.llm.GenerateChat(validKeys, systemPrompt, llmHistory)
	if err != nil {
		log.Printf("Error from Groq: %v\n", err)
		replyText = h.i18n.Get(lang, "error_occurred")
	} else {
		h.db.SaveMessage(h.BotUser.ID, m.Chat.ID, m.MessageThreadID, "assistant", replyText)
	}

	h.sendMsg(m.Chat.ID, m.MessageThreadID, m.MessageID, replyText, true, nil)
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

// State-Aware HTML Parser: Anti-Crash Mechanism
func formatToHTML(text string) string {
	codeBlocks := make(map[string]string)

	// 1. Karantina Multi-line Code Block
	reCodeBlock := regexp.MustCompile("(?s)```(.*?)```")
	text = reCodeBlock.ReplaceAllStringFunc(text, func(m string) string {
		placeholder := fmt.Sprintf("%%CB%d%%", len(codeBlocks))
		inner := m[3 : len(m)-3]
		inner = strings.ReplaceAll(inner, "&", "&amp;")
		inner = strings.ReplaceAll(inner, "<", "&lt;")
		inner = strings.ReplaceAll(inner, ">", "&gt;")
		codeBlocks[placeholder] = "<pre><code>" + inner + "</code></pre>"
		return placeholder
	})

	// 2. Karantina Inline Code Block
	reInlineCode := regexp.MustCompile("`([^`\n]+)`")
	text = reInlineCode.ReplaceAllStringFunc(text, func(m string) string {
		placeholder := fmt.Sprintf("%%IC%d%%", len(codeBlocks))
		inner := m[1 : len(m)-1]
		inner = strings.ReplaceAll(inner, "&", "&amp;")
		inner = strings.ReplaceAll(inner, "<", "&lt;")
		inner = strings.ReplaceAll(inner, ">", "&gt;")
		codeBlocks[placeholder] = "<code>" + inner + "</code>"
		return placeholder
	})

	// 3. Sanitasi karakter HTML standar untuk keamanan Telegram
	text = strings.ReplaceAll(text, "&", "&amp;")
	text = strings.ReplaceAll(text, "<", "&lt;")
	text = strings.ReplaceAll(text, ">", "&gt;")

	// 4. Lindungi Bullet Points (titik list) agar tidak salah diubah jadi Italic
	reBullet := regexp.MustCompile(`(?m)^(\s*)\* `)
	text = reBullet.ReplaceAllString(text, "$1%%BULLET%% ")

	// 5. Proses Bold & Italic HANYA jika mereka tidak menyeberangi tag HTML buatan kita (< dan >)
	// Ini menjamin mustahil terjadi tabrakan antara </i> dan </b>
	reBoldItalic := regexp.MustCompile(`\*\*\*([^<>\n]+?)\*\*\*`)
	text = reBoldItalic.ReplaceAllString(text, "<b><i>$1</i></b>")

	reBold := regexp.MustCompile(`\*\*([^<>\n]+?)\*\*`)
	text = reBold.ReplaceAllString(text, "<b>$1</b>")

	reItalic := regexp.MustCompile(`\*([^<>\*\n]+?)\*`)
	text = reItalic.ReplaceAllString(text, "<i>$1</i>")

	reUnderline := regexp.MustCompile(`\_\_([^<>\n]+?)\_\_`)
	text = reUnderline.ReplaceAllString(text, "<u>$1</u>")

	reItalicUnder := regexp.MustCompile(`\_([^<>\_\n]+?)\_`)
	text = reItalicUnder.ReplaceAllString(text, "<i>$1</i>")

	// Kembalikan Bullet Points yang dilindungi
	text = strings.ReplaceAll(text, "%%BULLET%%", "*")

	// Kembalikan semua Code Blocks yang dikarantina
	for placeholder, htmlCode := range codeBlocks {
		text = strings.ReplaceAll(text, placeholder, htmlCode)
	}

	return text
}

func (h *Handler) handleCallbackQuery(cq *CallbackQuery) {
	h.tg.AnswerCallbackQuery(cq.ID)

	lang := h.db.GetUserLang(cq.From.ID)
	parts := strings.Split(cq.Data, "_")

	if len(parts) == 2 && parts[0] == "lang" {
		newLang := parts[1]
		h.db.SetUserLang(cq.From.ID, newLang)
		if cq.Message != nil {
			h.tg.EditMessageText(EditMessageTextReq{
				ChatID:    cq.Message.Chat.ID,
				MessageID: cq.Message.MessageID,
				Text:      h.i18n.Get(newLang, "lang_changed"),
			})
		}
		return
	}

	if parts[0] == "action" {
		switch parts[1] {
		case "create":
			h.handleCreateBotFlow(cq.Message.Chat.ID, cq.Message.MessageThreadID, lang)
		case "mybots":
			h.sendMyBots(cq.From.ID, cq.Message.Chat.ID, cq.Message.MessageThreadID, lang)
		case "help":
			h.sendMsg(cq.Message.Chat.ID, cq.Message.MessageThreadID, 0, h.i18n.Get(lang, "help_message"), true, nil)
		case "setapi":
			h.handleSetApiFlow(cq.From.ID, cq.Message.Chat.ID, cq.Message.MessageThreadID, lang)
		case "cancelapi":
			h.stateMu.Lock()
			delete(h.userStates, cq.From.ID)
			h.stateMu.Unlock()
			h.sendMsg(cq.Message.Chat.ID, cq.Message.MessageThreadID, 0, h.i18n.Get(lang, "action_canceled"), true, nil)
		}
	}
}

func (h *Handler) handleCreateBotFlow(chatID int64, threadID int, lang string) {
	link := fmt.Sprintf("https://t.me/newbot/%s/my_new_bot", h.BotUser.Username)
	btnTxt := h.i18n.Get(lang, "btn_go_create")

	markup := InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{{Text: btnTxt, URL: link}},
		},
	}
	h.sendMsg(chatID, threadID, 0, h.i18n.Get(lang, "create_bot_instruction"), true, markup)
}

func (h *Handler) handleSetApiFlow(userID int64, chatID int64, threadID int, lang string) {
	encryptedKeys := h.db.GetUserAPIKeys(userID)
	currentKeys := "None"
	if encryptedKeys != "" {
		decrypted, err := crypto.Decrypt(encryptedKeys, h.EncryptionKey)
		if err == nil && decrypted != "" {
			currentKeys = decrypted
		}
	}

	text := fmt.Sprintf(h.i18n.Get(lang, "set_api_instruction"), currentKeys)

	btnCancel := h.i18n.Get(lang, "btn_cancel")
	markup := InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{{Text: btnCancel, CallbackData: "action_cancelapi"}},
		},
	}

	h.stateMu.Lock()
	h.userStates[userID] = "awaiting_api_key"
	h.stateMu.Unlock()

	h.sendMsg(chatID, threadID, 0, text, true, markup)
}

func (h *Handler) sendMyBots(userID int64, chatID int64, threadID int, lang string) {
	bots := h.db.GetBotsByOwner(userID)
	if len(bots) == 0 {
		h.sendMsg(chatID, threadID, 0, h.i18n.Get(lang, "no_bots_msg"), true, nil)
		return
	}

	msg := h.i18n.Get(lang, "my_bots_msg")
	var buttons [][]InlineKeyboardButton

	for _, bot := range bots {
		botUrl := "https://t.me/" + bot.Username
		buttons = append(buttons, []InlineKeyboardButton{
			{Text: "@" + bot.Username, URL: botUrl},
		})
	}

	markup := InlineKeyboardMarkup{
		InlineKeyboard: buttons,
	}

	h.sendMsg(chatID, threadID, 0, msg, true, markup)
}

func (h *Handler) sendMainMenu(chatID int64, threadID int, lang string, text string) {
	btnCreate := h.i18n.Get(lang, "btn_create")
	btnMyBots := h.i18n.Get(lang, "btn_mybots")
	btnHelp := h.i18n.Get(lang, "btn_help")
	btnSetApi := h.i18n.Get(lang, "btn_setapi")

	markup := InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: btnCreate, CallbackData: "action_create"},
				{Text: btnSetApi, CallbackData: "action_setapi"},
			},
			{
				{Text: btnMyBots, CallbackData: "action_mybots"},
				{Text: btnHelp, CallbackData: "action_help"},
			},
		},
	}

	h.sendMsg(chatID, threadID, 0, text, true, markup)
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

	h.sendMsg(chatID, threadID, 0, text, true, markup)
}

func (h *Handler) sendMsg(chatID int64, threadID int, replyToID int, text string, useHTML bool, markup interface{}) {
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

		if end == len(runes) && markup != nil {
			req.ReplyMarkup = markup
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