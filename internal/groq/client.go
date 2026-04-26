package groq

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
	httpClient *http.Client
}

type ChatRequest struct {
	Model       string    `json:"model"`
	Messages    []Message `json:"messages"`
	Temperature float64   `json:"temperature"`
	Stream      bool      `json:"stream"`
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type ChatResponse struct {
	Choices []Choice `json:"choices"`
}

type Choice struct {
	Message Message `json:"message"`
}

func New() *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *Client) GenerateChat(apiKeys []string, systemPrompt string, history []Message, model string) (string, error) {
	if len(apiKeys) == 0 {
		return "", fmt.Errorf("no api keys provided by user")
	}

	if model == "" {
		model = "openai/gpt-oss-120b"
	}

	url := "https://api.groq.com/openai/v1/chat/completions"

	var finalHistory []Message
	if systemPrompt != "" {
		finalHistory = append(finalHistory, Message{
			Role:    "system",
			Content: systemPrompt,
		})
	}
	finalHistory = append(finalHistory, history...)

	reqBody := ChatRequest{
		Model:       model,
		Messages:    finalHistory,
		Temperature: 0.7,
		Stream:      false,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		log.Printf("Failed to marshal Groq request: %v\n", err)
		return "", err
	}

	maxRetries := len(apiKeys) * 3
	baseDelay := 2 * time.Second
	var lastErr error
	var bodyBytes []byte

	for attempt := 0; attempt <= maxRetries; attempt++ {
		currentKey := apiKeys[attempt%len(apiKeys)]

		req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
		if err != nil {
			log.Printf("Failed to create Groq request: %v\n", err)
			return "", err
		}

		req.Header.Set("Authorization", "Bearer "+currentKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			log.Printf("Network error during Groq request: %v\n", err)
			time.Sleep(baseDelay)
			continue
		}

		bodyBytes, err = io.ReadAll(resp.Body)
		resp.Body.Close()
		if err != nil {
			lastErr = err
			log.Printf("Failed to read Groq response body: %v\n", err)
			continue
		}

		if resp.StatusCode == http.StatusOK {
			lastErr = nil
			break
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			lastErr = fmt.Errorf("Groq API returned status %d: %s", resp.StatusCode, string(bodyBytes))
			log.Printf("Rate limit hit. Rotating key statically...\n")
			time.Sleep(baseDelay)
			continue
		}

		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("Groq API returned status %d: %s", resp.StatusCode, string(bodyBytes))
			sleepDuration := time.Duration(1<<uint(attempt)) * baseDelay
			log.Printf("Server error %d. Retrying in %v...\n", resp.StatusCode, sleepDuration)
			time.Sleep(sleepDuration)
			continue
		}

		return "", fmt.Errorf("Groq API returned fatal status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	if lastErr != nil {
		return "", lastErr
	}

	var chatResp ChatResponse
	err = json.Unmarshal(bodyBytes, &chatResp)
	if err != nil {
		log.Printf("Failed to unmarshal Groq response: %v\n", err)
		return "", err
	}

	if len(chatResp.Choices) > 0 {
		return chatResp.Choices[0].Message.Content, nil
	}

	return "", fmt.Errorf("empty response from Groq")
}