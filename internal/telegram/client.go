package telegram

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

type Client struct {
	token      string
	baseURL    string
	httpClient *http.Client
}

type Update struct {
	UpdateID      int                `json:"update_id"`
	Message       *Message           `json:"message,omitempty"`
	CallbackQuery *CallbackQuery     `json:"callback_query,omitempty"`
	ManagedBot    *ManagedBotUpdated `json:"managed_bot,omitempty"`
	PreCheckoutQuery *PreCheckoutQuery `json:"pre_checkout_query,omitempty"`
}

type Message struct {
	MessageID         int                `json:"message_id"`
	MessageThreadID   int                `json:"message_thread_id,omitempty"`
	From              User               `json:"from"`
	Chat              Chat               `json:"chat"`
	Text              string             `json:"text,omitempty"`
	IsTopicMessage    bool               `json:"is_topic_message,omitempty"`
	ForumTopicCreated *ForumTopicCreated `json:"forum_topic_created,omitempty"`
	ReplyToMessage    *Message           `json:"reply_to_message,omitempty"`
	SuccessfulPayment *SuccessfulPayment `json:"successful_payment,omitempty"`
}

type ManagedBotUpdated struct {
	User User `json:"user"`
	Bot  User `json:"bot"`
}

type ForumTopicCreated struct {
	Name              string `json:"name"`
	IconColor         int    `json:"icon_color"`
	IconCustomEmojiID string `json:"icon_custom_emoji_id,omitempty"`
	IsNameImplicit    bool   `json:"is_name_implicit,omitempty"`
}

type CallbackQuery struct {
	ID      string   `json:"id"`
	From    User     `json:"from"`
	Message *Message `json:"message,omitempty"`
	Data    string   `json:"data"`
}

type User struct {
	ID               int64  `json:"id"`
	IsBot            bool   `json:"is_bot"`
	FirstName        string `json:"first_name"`
	Username         string `json:"username,omitempty"`
	HasTopicsEnabled bool   `json:"has_topics_enabled,omitempty"`
	CanManageBots    bool   `json:"can_manage_bots,omitempty"`
}

type Chat struct {
	ID   int64  `json:"id"`
	Type string `json:"type"`
}

type SendMessageReq struct {
	ChatID           int64       `json:"chat_id"`
	MessageThreadID  int         `json:"message_thread_id,omitempty"`
	ReplyToMessageID int         `json:"reply_to_message_id,omitempty"`
	Text             string      `json:"text"`
	ReplyMarkup      interface{} `json:"reply_markup,omitempty"`
	ParseMode        string      `json:"parse_mode,omitempty"`
}

type EditMessageTextReq struct {
	ChatID      int64       `json:"chat_id"`
	MessageID   int         `json:"message_id"`
	Text        string      `json:"text"`
	ParseMode   string      `json:"parse_mode,omitempty"`
	ReplyMarkup interface{} `json:"reply_markup,omitempty"`
}

type SendChatActionReq struct {
	ChatID          int64  `json:"chat_id"`
	MessageThreadID int    `json:"message_thread_id,omitempty"`
	Action          string `json:"action"`
}

type InlineKeyboardMarkup struct {
	InlineKeyboard [][]InlineKeyboardButton `json:"inline_keyboard"`
}

type InlineKeyboardButton struct {
	Text         string `json:"text"`
	CallbackData string `json:"callback_data,omitempty"`
	URL          string `json:"url,omitempty"`
}

func NewClient(token string) *Client {
	return &Client{
		token:   token,
		baseURL: fmt.Sprintf("https://api.telegram.org/bot%s", token),
		httpClient: &http.Client{
			Timeout: 60 * time.Second,
		},
	}
}

func (c *Client) GetMe() (*User, error) {
	url := fmt.Sprintf("%s/getMe", c.baseURL)
	resp, err := c.httpClient.Get(url)
	if err != nil {
		log.Printf("Error fetching bot info: %v\n", err)
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var result struct {
		Ok     bool `json:"ok"`
		Result User `json:"result"`
	}

	err = json.Unmarshal(body, &result)
	if err != nil {
		return nil, err
	}

	if !result.Ok {
		return nil, fmt.Errorf("telegram API getMe returned ok=false")
	}

	return &result.Result, nil
}

func (c *Client) GetUpdates(offset int) ([]Update, error) {
	url := fmt.Sprintf("%s/getUpdates?offset=%d&timeout=30", c.baseURL, offset)
	resp, err := c.httpClient.Get(url)
	if err != nil {
		log.Printf("Error fetching updates: %v\n", err)
		return nil, err
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		log.Printf("Error reading updates body: %v\n", err)
		return nil, err
	}

	var result struct {
		Ok     bool     `json:"ok"`
		Result []Update `json:"result"`
	}

	err = json.Unmarshal(body, &result)
	if err != nil {
		log.Printf("Error unmarshaling updates: %v\n", err)
		return nil, err
	}

	if !result.Ok {
		err := fmt.Errorf("telegram API returned ok=false: %s", string(body))
		log.Printf("%v\n", err)
		return nil, err
	}

	return result.Result, nil
}

func (c *Client) GetManagedBotToken(botID int64) (string, error) {
	url := fmt.Sprintf("%s/getManagedBotToken", c.baseURL)
	reqBody := map[string]int64{"user_id": botID}
	jsonData, _ := json.Marshal(reqBody)

	resp, err := c.httpClient.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	var result struct {
		Ok     bool   `json:"ok"`
		Result string `json:"result"`
	}

	err = json.Unmarshal(body, &result)
	if err != nil {
		return "", err
	}

	if !result.Ok {
		return "", fmt.Errorf("failed to get managed bot token: %s", string(body))
	}

	return result.Result, nil
}

func (c *Client) SendMessage(req SendMessageReq) error {
	url := fmt.Sprintf("%s/sendMessage", c.baseURL)
	jsonData, err := json.Marshal(req)
	if err != nil {
		log.Printf("Error marshaling send message request: %v\n", err)
		return err
	}

	resp, err := c.httpClient.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("Error sending message: %v\n", err)
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		err := fmt.Errorf("telegram returned status %d: %s", resp.StatusCode, string(body))
		log.Printf("%v\n", err)
		return err
	}
	return nil
}

func (c *Client) EditMessageText(req EditMessageTextReq) error {
	url := fmt.Sprintf("%s/editMessageText", c.baseURL)
	jsonData, err := json.Marshal(req)
	if err != nil {
		log.Printf("Error marshaling edit message request: %v\n", err)
		return err
	}

	resp, err := c.httpClient.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("Error editing message: %v\n", err)
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (c *Client) SendChatAction(req SendChatActionReq) error {
	url := fmt.Sprintf("%s/sendChatAction", c.baseURL)
	jsonData, err := json.Marshal(req)
	if err != nil {
		log.Printf("Error marshaling chat action request: %v\n", err)
		return err
	}

	resp, err := c.httpClient.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		log.Printf("Error sending chat action: %v\n", err)
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (c *Client) AnswerCallbackQuery(callbackQueryID string) {
	url := fmt.Sprintf("%s/answerCallbackQuery", c.baseURL)
	reqBody := map[string]string{"callback_query_id": callbackQueryID}
	jsonData, _ := json.Marshal(reqBody)
	c.httpClient.Post(url, "application/json", bytes.NewBuffer(jsonData))
}

type PreCheckoutQuery struct {
	ID             string `json:"id"`
	From           User   `json:"from"`
	Currency       string `json:"currency"`
	TotalAmount    int    `json:"total_amount"`
	InvoicePayload string `json:"invoice_payload"`
}

type SuccessfulPayment struct {
	Currency                string `json:"currency"`
	TotalAmount             int    `json:"total_amount"`
	InvoicePayload          string `json:"invoice_payload"`
	TelegramPaymentChargeID string `json:"telegram_payment_charge_id"`
	ProviderPaymentChargeID string `json:"provider_payment_charge_id"`
}

type LabeledPrice struct {
	Label  string `json:"label"`
	Amount int    `json:"amount"`
}

type SendInvoiceReq struct {
	ChatID        int64          `json:"chat_id"`
	Title         string         `json:"title"`
	Description   string         `json:"description"`
	Payload       string         `json:"payload"`
	ProviderToken string         `json:"provider_token"`
	Currency      string         `json:"currency"`
	Prices        []LabeledPrice `json:"prices"`
}

type AnswerPreCheckoutQueryReq struct {
	PreCheckoutQueryID string `json:"pre_checkout_query_id"`
	Ok                 bool   `json:"ok"`
	ErrorMessage       string `json:"error_message,omitempty"`
}

func (c *Client) SendInvoice(req SendInvoiceReq) error {
	url := fmt.Sprintf("%s/sendInvoice", c.baseURL)
	jsonData, _ := json.Marshal(req)
	resp, err := c.httpClient.Post(url, "application/json", bytes.NewBuffer(jsonData))
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return nil
}

func (c *Client) AnswerPreCheckoutQuery(req AnswerPreCheckoutQueryReq) {
	url := fmt.Sprintf("%s/answerPreCheckoutQuery", c.baseURL)
	jsonData, _ := json.Marshal(req)
	c.httpClient.Post(url, "application/json", bytes.NewBuffer(jsonData))
}