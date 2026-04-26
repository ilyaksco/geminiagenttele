package telegram

import (
	"fmt"
	"gemini-agent/internal/crypto"
	"gemini-agent/internal/database"
	"gemini-agent/internal/groq"
	"gemini-agent/internal/i18n"
	"gemini-agent/config"
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
	PremiumCfg    *config.PremiumConfig
	BotUser       *User
	EncryptionKey string
	IsManager     bool
	OnNewBot      func(botID int64, token string)
	OnDeleteBot   func(botID int64)
	userStates    map[int64]string
	stateMu       sync.RWMutex
	botTracker    *BotTracker
}

func NewHandler(tg *Client, db *database.DB, llm *groq.Client, i18n *i18n.I18n, premiumCfg *config.PremiumConfig, botUser *User, encryptionKey string, isManager bool, onNewBot func(botID int64, token string)) *Handler {
	return &Handler{
		tg:            tg,
		db:            db,
		llm:           llm,
		i18n:          i18n,
		PremiumCfg:    premiumCfg,
		BotUser:       botUser,
		EncryptionKey: encryptionKey,
		IsManager:     isManager,
		OnNewBot:      onNewBot,
		userStates:    make(map[int64]string),
		botTracker:    NewBotTracker(),
	}
}

func (h *Handler) HandleUpdate(u Update) {
	if u.ManagedBot != nil {
		go h.handleManagedBotUpdate(u.ManagedBot)
	} else if u.PreCheckoutQuery != nil {
		go h.handlePreCheckoutQuery(u.PreCheckoutQuery)
	} else if u.Message != nil {
		go h.handleMessage(u.Message)
	} else if u.CallbackQuery != nil {
		go h.handleCallbackQuery(u.CallbackQuery)
	}
}

func (h *Handler) handlePreCheckoutQuery(pcq *PreCheckoutQuery) {
	h.tg.AnswerPreCheckoutQuery(AnswerPreCheckoutQueryReq{
		PreCheckoutQueryID: pcq.ID,
		Ok:                 true,
	})
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

	tempClient := NewClient(token)
	me, err := tempClient.GetMe()
	
	botUsername := mb.Bot.Username
	botName := mb.Bot.FirstName

	if err == nil && me != nil {
		botUsername = me.Username
		botName = me.FirstName
	}

	h.db.SaveManagedBot(mb.Bot.ID, mb.User.ID, botUsername, botName, token)
	log.Printf("Successfully registered managed bot @%s (%s) created by User %d\n", botUsername, botName, mb.User.ID)

	if h.OnNewBot != nil {
		h.OnNewBot(mb.Bot.ID, token)
	}
}

func (h *Handler) sendBotDashboard(chatID int64, msgID int, botID int64, lang string) {
	bot := h.db.GetManagedBot(botID)
	if bot == nil {
		h.sendMainMenu(chatID, 0, msgID, lang, h.i18n.Get(lang, "error_occurred"))
		return
	}

	text := fmt.Sprintf("🤖 **Bot Dashboard**\n\n**Name:** %s\n**Prompt:** `%s`", bot.Name, bot.SystemPrompt)
	if bot.SystemPrompt == "" {
		text = fmt.Sprintf("🤖 **Bot Dashboard**\n\n**Name:** %s\n**Prompt:** _Not set_", bot.Name)
	}

	btnSetPrompt := "📝 Set Prompt"
	btnDelete := "🗑️ Delete Bot"
	btnBack := "🔙 Back"
	if lang == "id" {
		btnSetPrompt = "📝 Atur Prompt"
		btnDelete = "🗑️ Hapus Bot"
		btnBack = "🔙 Kembali"
	}

	markup := InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{{Text: "🚀 Open Bot", URL: "https://t.me/" + bot.Username}},
			{{Text: btnSetPrompt, CallbackData: fmt.Sprintf("bot_prompt_%d", botID)}},
			{{Text: btnDelete, CallbackData: fmt.Sprintf("bot_delete_%d", botID)}},
			{{Text: btnBack, CallbackData: "action_mybots"}},
		},
	}

	h.editMsg(chatID, msgID, text, true, markup)
}

func (h *Handler) handleDeleteBotFlow(chatID int64, msgID int, botID int64, lang string, confirm bool) {
	bot := h.db.GetManagedBot(botID)
	if bot == nil {
		h.sendMainMenu(chatID, 0, msgID, lang, h.i18n.Get(lang, "error_occurred"))
		return
	}

	if !confirm {
		text := fmt.Sprintf("⚠️ **Confirmation**\n\nAre you sure you want to delete **%s** (@%s)?\nThis action cannot be undone.", bot.Name, bot.Username)
		if lang == "id" {
			text = fmt.Sprintf("⚠️ **Konfirmasi**\n\nApakah Anda yakin ingin menghapus **%s** (@%s)?\nAksi ini tidak dapat dibatalkan.", bot.Name, bot.Username)
		}

		btnYes := "✅ Yes, Delete"
		btnNo := "❌ No, Cancel"
		if lang == "id" {
			btnYes = "✅ Ya, Hapus"
			btnNo = "❌ Tidak, Batal"
		}

		markup := InlineKeyboardMarkup{
			InlineKeyboard: [][]InlineKeyboardButton{
				{{Text: btnYes, CallbackData: fmt.Sprintf("bot_confirmdel_%d", botID)}},
				{{Text: btnNo, CallbackData: fmt.Sprintf("bot_manage_%d", botID)}},
			},
		}
		h.editMsg(chatID, msgID, text, true, markup)
		return
	}

	ownerID := h.db.GetBotOwner(botID)
	
	// 1. Stop the running bot instance first
	if h.OnDeleteBot != nil {
		h.OnDeleteBot(botID)
	}

	// 2. Delete from database
	h.db.DeleteManagedBot(botID, ownerID)

	// 3. Refresh the list for the correct owner
	h.sendMyBots(ownerID, chatID, 0, msgID, lang)
}

func (h *Handler) handleMessage(m *Message) {
	lang := h.db.GetUserLang(m.From.ID)

	if m.SuccessfulPayment != nil {
		if m.SuccessfulPayment.InvoicePayload == "premium_upgrade" {
			h.db.GrantPremium(m.From.ID, h.PremiumCfg.PremiumDurationDays)
			h.sendMsg(m.Chat.ID, m.MessageThreadID, m.MessageID, h.i18n.Get(lang, "premium_success"), true, nil)
		}
		return
	}

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

		if strings.HasPrefix(state, "awaiting_prompt_") {
			botIDStr := strings.TrimPrefix(state, "awaiting_prompt_")
			var targetBotID int64
			fmt.Sscanf(botIDStr, "%d", &targetBotID)

			success := h.db.SetBotPrompt(targetBotID, m.Text)
			h.stateMu.Lock()
			delete(h.userStates, m.From.ID)
			h.stateMu.Unlock()

			if success {
				h.sendMsg(m.Chat.ID, m.MessageThreadID, m.MessageID, "✅ Prompt updated successfully!", true, nil)
			} else {
				h.sendMsg(m.Chat.ID, m.MessageThreadID, m.MessageID, "❌ Failed to update prompt.", true, nil)
			}
			h.sendBotDashboard(m.Chat.ID, 0, targetBotID, lang)
			return
		}

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

			h.sendMainMenu(m.Chat.ID, m.MessageThreadID, 0, lang, h.i18n.Get(lang, "welcome"))
			return
		}

		if strings.HasPrefix(m.Text, "/start") {
			msg := h.i18n.Get(lang, "welcome")
			h.sendMainMenu(m.Chat.ID, m.MessageThreadID, 0, lang, msg)
			return
		}

		if strings.HasPrefix(m.Text, "/newchat") {
			err := h.db.ClearChatHistory(h.BotUser.ID, m.Chat.ID, m.MessageThreadID)
			if err != nil {
				h.sendMsg(m.Chat.ID, m.MessageThreadID, m.MessageID, "❌ Failed to clear history.", true, nil)
			} else {
				h.sendMsg(m.Chat.ID, m.MessageThreadID, m.MessageID, h.i18n.Get(lang, "chat_cleared"), true, nil)
			}
			return
		}

		if strings.HasPrefix(m.Text, "/link") {
			h.handleCreateBotFlow(m.From.ID, m.Chat.ID, m.MessageThreadID, 0, lang)
			return
		}

		if strings.HasPrefix(m.Text, "/premium") {
			h.sendPremiumMenu(m.Chat.ID, m.MessageThreadID, 0, lang)
			return
		}

		if strings.HasPrefix(m.Text, "/mybots") {
			h.sendMyBots(m.From.ID, m.Chat.ID, m.MessageThreadID, 0, lang)
			return
		}

		if strings.HasPrefix(m.Text, "/setapi") {
			h.handleSetApiFlow(m.From.ID, m.Chat.ID, m.MessageThreadID, 0, lang)
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
			h.sendHelpMenu(m.Chat.ID, m.MessageThreadID, 0, lang)
			return
		}

		if strings.HasPrefix(m.Text, "/lang") {
			h.sendLangMenu(m.Chat.ID, m.MessageThreadID, 0, lang)
			return
		}

		h.sendMsg(m.Chat.ID, m.MessageThreadID, m.MessageID, "⚠️ Use /start to open the menu.", true, nil)
		return
	}

	isPrivate := m.Chat.Type == "private"
	isMentioned := false
	if h.BotUser != nil {
		isMentioned = strings.Contains(m.Text, "@"+h.BotUser.Username)
	}
	isReplyToMe := h.BotUser != nil && m.ReplyToMessage != nil && m.ReplyToMessage.From.ID == h.BotUser.ID

	if isPrivate || isMentioned || isReplyToMe {

		ownerID := h.db.GetBotOwner(h.BotUser.ID)
		if ownerID == 0 && !h.IsManager {
			return
		}

		if m.From.IsBot {
			if !h.botTracker.AllowBotInteraction(m.Chat.ID, m.From.ID) {
				return
			}
		} else {
			h.botTracker.ResetChain(m.Chat.ID)
		}

		if strings.HasPrefix(m.Text, "/start") {
			msg := fmt.Sprintf("Hello, I am **%s**! How can I help you today?", h.BotUser.FirstName)
			if lang == "id" {
				msg = fmt.Sprintf("Halo, saya adalah **%s**! Apa yang bisa saya bantu hari ini?", h.BotUser.FirstName)
			}
			h.sendMsg(m.Chat.ID, m.MessageThreadID, m.MessageID, msg, true, nil)
			return
		}

		if strings.HasPrefix(m.Text, "/newchat") {
			err := h.db.ClearChatHistory(h.BotUser.ID, m.Chat.ID, m.MessageThreadID)
			if err != nil {
				h.sendMsg(m.Chat.ID, m.MessageThreadID, m.MessageID, "❌ Failed to clear history.", true, nil)
			} else {
				h.sendMsg(m.Chat.ID, m.MessageThreadID, m.MessageID, h.i18n.Get(lang, "chat_cleared"), true, nil)
			}
			return
		}

		h.tg.SendChatAction(SendChatActionReq{ChatID: m.Chat.ID, MessageThreadID: m.MessageThreadID, Action: "typing"})

		encryptedKeys := h.db.GetUserAPIKeys(ownerID)
		decryptedKeys := ""
		if encryptedKeys != "" {
			decryptedKeys, _ = crypto.Decrypt(encryptedKeys, h.EncryptionKey)
		}
		apiKeys := strings.Split(decryptedKeys, ",")
		var validKeys []string
		for _, k := range apiKeys {
			if k != "" {
				validKeys = append(validKeys, k)
			}
		}
		if len(validKeys) == 0 {
			h.sendMsg(m.Chat.ID, m.MessageThreadID, m.MessageID, h.i18n.Get(lang, "missing_api_key"), true, nil)
			return
		}

		fullMessage := m.Text
		if m.ReplyToMessage != nil && m.ReplyToMessage.Text != "" {
			targetName := m.ReplyToMessage.From.Username
			if targetName != "" {
				targetName = "@" + targetName
			} else {
				targetName = m.ReplyToMessage.From.FirstName
			}

			fullMessage = fmt.Sprintf("[Context - Replying to %s's message: \"%s\"]\n\n%s", targetName, m.ReplyToMessage.Text, m.Text)
		}

		h.db.SaveMessage(h.BotUser.ID, m.Chat.ID, m.MessageThreadID, "user", fullMessage)

		rawHistory := h.db.GetHistory(h.BotUser.ID, m.Chat.ID, m.MessageThreadID, 6)
		llmHistory := buildGroqHistory(rawHistory, fullMessage)
		systemPrompt := h.db.GetBotPrompt(h.BotUser.ID)

		replyText, err := h.llm.GenerateChat(validKeys, systemPrompt, llmHistory)
		if err != nil {
			replyText = h.i18n.Get(lang, "error_occurred")
		} else {
			h.db.SaveMessage(h.BotUser.ID, m.Chat.ID, m.MessageThreadID, "assistant", replyText)
		}
		h.sendMsg(m.Chat.ID, m.MessageThreadID, m.MessageID, replyText, true, nil)
	}
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

	msgID := 0
	if cq.Message != nil {
		msgID = cq.Message.MessageID
	}

	if parts[0] == "bot" && len(parts) >= 3 {
		botIDStr := parts[2]
		var targetBotID int64
		fmt.Sscanf(botIDStr, "%d", &targetBotID)

		switch parts[1] {
		case "manage":
			h.sendBotDashboard(cq.Message.Chat.ID, msgID, targetBotID, lang)
		case "prompt":
			text := "📝 **Set System Prompt**\n\nPlease send the new prompt text for this bot."
			if lang == "id" {
				text = "📝 **Setel System Prompt**\n\nSilakan kirim teks prompt baru untuk bot ini."
			}
			h.stateMu.Lock()
			h.userStates[cq.From.ID] = fmt.Sprintf("awaiting_prompt_%d", targetBotID)
			h.stateMu.Unlock()
			h.editMsg(cq.Message.Chat.ID, msgID, text, true, InlineKeyboardMarkup{
				InlineKeyboard: [][]InlineKeyboardButton{{{Text: "🔙 Back", CallbackData: fmt.Sprintf("bot_manage_%d", targetBotID)}}},
			})
		case "delete":
			h.handleDeleteBotFlow(cq.Message.Chat.ID, msgID, targetBotID, lang, false)
		case "confirmdel":
			h.handleDeleteBotFlow(cq.Message.Chat.ID, msgID, targetBotID, lang, true)
		}
		return
	}

	h.stateMu.Lock()
	delete(h.userStates, cq.From.ID)
	h.stateMu.Unlock()

	if len(parts) == 2 && parts[0] == "lang" {
		newLang := parts[1]
		h.db.SetUserLang(cq.From.ID, newLang)
		if msgID != 0 {
			h.sendMainMenu(cq.Message.Chat.ID, cq.Message.MessageThreadID, msgID, newLang, h.i18n.Get(newLang, "lang_changed")+"\n\n"+h.i18n.Get(newLang, "welcome"))
		}
		return
	}

	if parts[0] == "action" {
		switch parts[1] {
		case "create":
			h.handleCreateBotFlow(cq.From.ID, cq.Message.Chat.ID, cq.Message.MessageThreadID, msgID, lang)
		case "buypremium":
			title := h.i18n.Get(lang, "premium_invoice_title")
			desc := fmt.Sprintf(h.i18n.Get(lang, "premium_invoice_desc"), h.PremiumCfg.PremiumDurationDays)
			h.tg.SendInvoice(SendInvoiceReq{
				ChatID:      cq.Message.Chat.ID,
				Title:       title,
				Description: desc,
				Payload:     "premium_upgrade",
				Currency:    "XTR",
				Prices:      []LabeledPrice{{Label: title, Amount: h.PremiumCfg.PremiumPriceStars}},
			})
		case "mybots":
			h.sendMyBots(cq.From.ID, cq.Message.Chat.ID, cq.Message.MessageThreadID, msgID, lang)
		case "help":
			h.sendHelpMenu(cq.Message.Chat.ID, cq.Message.MessageThreadID, msgID, lang)
		case "setapi":
			h.handleSetApiFlow(cq.From.ID, cq.Message.Chat.ID, cq.Message.MessageThreadID, msgID, lang)
		case "tutorialapi":
			h.sendApiTutorial(cq.Message.Chat.ID, msgID, lang)
		case "lang":
			h.sendLangMenu(cq.Message.Chat.ID, cq.Message.MessageThreadID, msgID, lang)
		case "premium":
			h.sendPremiumMenu(cq.Message.Chat.ID, cq.Message.MessageThreadID, msgID, lang)
		case "back", "cancelapi":
			h.sendMainMenu(cq.Message.Chat.ID, cq.Message.MessageThreadID, msgID, lang, h.i18n.Get(lang, "welcome"))
		}
	}
}

func (h *Handler) handleCreateBotFlow(userID int64, chatID int64, threadID int, msgID int, lang string) {
	botCount := h.db.CountUserBots(userID)
	isPremium := h.db.IsUserPremium(userID)

	if botCount >= h.PremiumCfg.MaxFreeBots && !isPremium {
		text := fmt.Sprintf(h.i18n.Get(lang, "limit_reached"), h.PremiumCfg.MaxFreeBots)
		btnUpgrade := fmt.Sprintf(h.i18n.Get(lang, "btn_buy_premium_stars"), h.PremiumCfg.PremiumPriceStars)
		btnBack := "🔙 Back"
		if lang == "id" {
			btnBack = "🔙 Kembali"
		}

		markup := InlineKeyboardMarkup{
			InlineKeyboard: [][]InlineKeyboardButton{
				{{Text: btnUpgrade, CallbackData: "action_buypremium"}},
				{{Text: btnBack, CallbackData: "action_back"}},
			},
		}

		if msgID == 0 {
			h.sendMsg(chatID, threadID, 0, text, true, markup)
		} else {
			h.editMsg(chatID, msgID, text, true, markup)
		}
		return
	}

	suggestedUsername := fmt.Sprintf("%s_", h.BotUser.Username)
	link := fmt.Sprintf("https://t.me/newbot/%s/%s", h.BotUser.Username, suggestedUsername)
	
	btnTxt := h.i18n.Get(lang, "btn_go_create")
	btnBack := "🔙 Back"
	if lang == "id" {
		btnBack = "🔙 Kembali"
	}

	markup := InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{{Text: btnTxt, URL: link}},
			{{Text: btnBack, CallbackData: "action_back"}},
		},
	}
	if msgID == 0 {
		h.sendMsg(chatID, threadID, 0, h.i18n.Get(lang, "create_bot_instruction"), true, markup)
	} else {
		h.editMsg(chatID, msgID, h.i18n.Get(lang, "create_bot_instruction"), true, markup)
	}
}

func (h *Handler) handleSetApiFlow(userID int64, chatID int64, threadID int, msgID int, lang string) {
	encryptedKeys := h.db.GetUserAPIKeys(userID)
	currentKeys := "None"
	if encryptedKeys != "" {
		decrypted, err := crypto.Decrypt(encryptedKeys, h.EncryptionKey)
		if err == nil && decrypted != "" {
			currentKeys = decrypted
		}
	}

	text := fmt.Sprintf(h.i18n.Get(lang, "set_api_instruction"), currentKeys)
	btnTutorial := h.i18n.Get(lang, "btn_how_to_get_api")
	btnCancel := h.i18n.Get(lang, "btn_cancel")

	markup := InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{{Text: btnTutorial, CallbackData: "action_tutorialapi"}},
			{{Text: btnCancel, CallbackData: "action_back"}},
		},
	}

	h.stateMu.Lock()
	h.userStates[userID] = "awaiting_api_key"
	h.stateMu.Unlock()

	if msgID == 0 {
		h.sendMsg(chatID, threadID, 0, text, true, markup)
	} else {
		h.editMsg(chatID, msgID, text, true, markup)
	}
}

func (h *Handler) sendApiTutorial(chatID int64, msgID int, lang string) {
	title := h.i18n.Get(lang, "api_tutorial_title")
	steps := h.i18n.Get(lang, "api_tutorial_steps")
	fullMsg := title + "\n\n" + steps

	btnOpen := h.i18n.Get(lang, "btn_open_groq")
	btnBack := "🔙 Back"
	if lang == "id" {
		btnBack = "🔙 Kembali"
	}

	markup := InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{{Text: btnOpen, URL: "https://console.groq.com/keys"}},
			{{Text: btnBack, CallbackData: "action_setapi"}},
		},
	}

	h.editMsg(chatID, msgID, fullMsg, true, markup)
}

func (h *Handler) sendMyBots(userID int64, chatID int64, threadID int, msgID int, lang string) {
	bots := h.db.GetBotsByOwner(userID)
	btnBack := "🔙 Back"
	if lang == "id" {
		btnBack = "🔙 Kembali"
	}
	
	var buttons [][]InlineKeyboardButton

	if len(bots) == 0 {
		buttons = append(buttons, []InlineKeyboardButton{{Text: btnBack, CallbackData: "action_back"}})
		markup := InlineKeyboardMarkup{InlineKeyboard: buttons}
		if msgID == 0 {
			h.sendMsg(chatID, threadID, 0, h.i18n.Get(lang, "no_bots_msg"), true, markup)
		} else {
			h.editMsg(chatID, msgID, h.i18n.Get(lang, "no_bots_msg"), true, markup)
		}
		return
	}

	msg := h.i18n.Get(lang, "my_bots_msg")
	for _, bot := range bots {
		displayName := bot.Name
		if displayName == "" {
			displayName = bot.Username
		}
		buttons = append(buttons, []InlineKeyboardButton{
			{Text: "🤖 " + displayName, CallbackData: fmt.Sprintf("bot_manage_%d", bot.BotID)},
		})
	}
	buttons = append(buttons, []InlineKeyboardButton{{Text: btnBack, CallbackData: "action_back"}})

	markup := InlineKeyboardMarkup{
		InlineKeyboard: buttons,
	}

	if msgID == 0 {
		h.sendMsg(chatID, threadID, 0, msg, true, markup)
	} else {
		h.editMsg(chatID, msgID, msg, true, markup)
	}
}

func (h *Handler) sendMainMenu(chatID int64, threadID int, msgID int, lang string, text string) {
	btnCreate := h.i18n.Get(lang, "btn_create")
	btnMyBots := h.i18n.Get(lang, "btn_mybots")
	btnHelp := h.i18n.Get(lang, "btn_help")
	btnSetApi := h.i18n.Get(lang, "btn_setapi")
	btnLang := "🌐 Language"
	btnPremium := "🌟 Premium"

	markup := InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: btnCreate, CallbackData: "action_create"},
				{Text: btnSetApi, CallbackData: "action_setapi"},
			},
			{
				{Text: btnMyBots, CallbackData: "action_mybots"},
				{Text: btnLang, CallbackData: "action_lang"},
			},
			{
				{Text: btnHelp, CallbackData: "action_help"},
				{Text: btnPremium, CallbackData: "action_premium"},
			},
		},
	}

	if msgID == 0 {
		h.sendMsg(chatID, threadID, 0, text, true, markup)
	} else {
		h.editMsg(chatID, msgID, text, true, markup)
	}
}

func (h *Handler) sendLangMenu(chatID int64, threadID int, msgID int, lang string) {
	text := h.i18n.Get(lang, "choose_lang")
	btnEn := h.i18n.Get(lang, "btn_en")
	btnId := h.i18n.Get(lang, "btn_id")
	btnBack := "🔙 Back"
	if lang == "id" {
		btnBack = "🔙 Kembali"
	}

	markup := InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{
				{Text: btnEn, CallbackData: "lang_en"},
				{Text: btnId, CallbackData: "lang_id"},
			},
			{
				{Text: btnBack, CallbackData: "action_back"},
			},
		},
	}

	if msgID == 0 {
		h.sendMsg(chatID, threadID, 0, text, true, markup)
	} else {
		h.editMsg(chatID, msgID, text, true, markup)
	}
}

func (h *Handler) sendMsg(chatID int64, threadID int, replyToID int, text string, useHTML bool, markup interface{}) {
	if useHTML {
		text = formatToHTML(text)
	}

	var chunks []string
	if useHTML {
		// Menggunakan Smart HTML Splitter
		chunks = h.splitHTMLChunks(text, 4000)
	} else {
		// Pemotongan dasar jika tidak memakai format HTML
		runes := []rune(text)
		for i := 0; i < len(runes); i += 4000 {
			end := i + 4000
			if end > len(runes) {
				end = len(runes)
			}
			chunks = append(chunks, string(runes[i:end]))
		}
	}

	for i, chunk := range chunks {
		req := SendMessageReq{
			ChatID:           chatID,
			MessageThreadID:  threadID,
			ReplyToMessageID: replyToID,
			Text:             chunk,
		}

		if useHTML {
			req.ParseMode = "HTML"
		}

		// Tombol/markup hanya dipasang di pesan potongan paling akhir
		if i == len(chunks)-1 && markup != nil {
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

		// Reply ID hanya di-set pada potongan pertama, potongan lanjutannya tidak perlu mereply ulang
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

func (h *Handler) editMsg(chatID int64, msgID int, text string, useHTML bool, markup interface{}) {
	if useHTML {
		text = formatToHTML(text)
	}
	req := EditMessageTextReq{
		ChatID:      chatID,
		MessageID:   msgID,
		Text:        text,
	}
	if useHTML {
		req.ParseMode = "HTML"
	}
	if markup != nil {
		req.ReplyMarkup = markup
	}
	
	err := h.tg.EditMessageText(req)
	if err != nil && useHTML {
		log.Printf("Failed to edit HTML, falling back to plain text for chat %d: %v\n", chatID, err)
		req.ParseMode = ""
		rawRunes := []rune(h.stripHTML(text))
		req.Text = string(rawRunes)
		_ = h.tg.EditMessageText(req)
	} else if err != nil {
		log.Printf("Failed to edit message to chat %d: %v\n", chatID, err)
	}
}

func (h *Handler) sendHelpMenu(chatID int64, threadID int, msgID int, lang string) {
	msg := h.i18n.Get(lang, "help_message")
	btnBack := "🔙 Back"
	if lang == "id" {
		btnBack = "🔙 Kembali"
	}

	markup := InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{{Text: btnBack, CallbackData: "action_back"}},
		},
	}

	if msgID == 0 {
		h.sendMsg(chatID, threadID, 0, msg, true, markup)
	} else {
		h.editMsg(chatID, msgID, msg, true, markup)
	}
}

func (h *Handler) splitHTMLChunks(text string, limit int) []string {
	var chunks []string
	runes := []rune(text)

	for len(runes) > 0 {
		if len(runes) <= limit {
			chunks = append(chunks, string(runes))
			break
		}

		// Cari batas potong optimal (spasi atau baris baru) agar tidak memotong di tengah kata
		splitIdx := limit
		for i := limit; i > limit-1000 && i > 0; i-- {
			if runes[i] == '\n' || runes[i] == ' ' {
				splitIdx = i
				break
			}
		}

		chunk := string(runes[:splitIdx])
		openTags := h.getOpenHTMLTags(chunk)

		// Tutup paksa tag yang masih terbuka di akhir chunk ini (dari urutan terakhir ke pertama)
		for i := len(openTags) - 1; i >= 0; i-- {
			chunk += "</" + openTags[i] + ">"
		}
		chunks = append(chunks, chunk)

		// Buka kembali tag tersebut di awal chunk berikutnya
		nextPrefix := ""
		for _, tag := range openTags {
			nextPrefix += "<" + tag + ">"
		}

		// Lanjutkan sisa teks yang belum dipotong
		runes = append([]rune(nextPrefix), runes[splitIdx:]...)
	}

	return chunks
}

func (h *Handler) getOpenHTMLTags(text string) []string {
	var stack []string
	re := regexp.MustCompile(`</?(b|i|u|code|pre)>`)
	matches := re.FindAllStringSubmatch(text, -1)
	for _, match := range matches {
		tag := match[1]
		if strings.HasPrefix(match[0], "</") {
			// Hapus tag dari tumpukan jika sudah ditutup
			if len(stack) > 0 && stack[len(stack)-1] == tag {
				stack = stack[:len(stack)-1]
			} else {
				for i := len(stack) - 1; i >= 0; i-- {
					if stack[i] == tag {
						stack = append(stack[:i], stack[i+1:]...)
						break
					}
				}
			}
		} else {
			stack = append(stack, tag)
		}
	}
	return stack
}

func (h *Handler) sendPremiumMenu(chatID int64, threadID int, msgID int, lang string) {
	title := h.i18n.Get(lang, "premium_menu_title")
	features := h.i18n.Get(lang, "premium_features_list")
	fullMsg := title + "\n\n" + features

	btnBuy := fmt.Sprintf(h.i18n.Get(lang, "btn_buy_premium_xtr"), h.PremiumCfg.PremiumPriceStars)
	btnBack := "🔙 Back"
	if lang == "id" {
		btnBack = "🔙 Kembali"
	}

	markup := InlineKeyboardMarkup{
		InlineKeyboard: [][]InlineKeyboardButton{
			{{Text: btnBuy, CallbackData: "action_buypremium"}},
			{{Text: btnBack, CallbackData: "action_back"}},
		},
	}

	if msgID == 0 {
		h.sendMsg(chatID, threadID, 0, fullMsg, true, markup)
	} else {
		h.editMsg(chatID, msgID, fullMsg, true, markup)
	}
}