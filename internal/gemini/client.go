package gemini

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"strings"
	"time"
)

type Client struct {
	httpClient *http.Client
}

type Part struct {
	Text string `json:"text"`
}

type Message struct {
	Role  string `json:"role"`
	Parts []Part `json:"parts"`
}

type SystemInstruction struct {
	Parts []Part `json:"parts"`
}

// (pembaruan 4)
type ThinkingConfig struct {
	ThinkingLevel string `json:"thinkingLevel,omitempty"`
}

type GenerationConfig struct {
	ThinkingConfig *ThinkingConfig `json:"thinkingConfig,omitempty"`
}

type ChatRequest struct {
	SystemInstruction *SystemInstruction `json:"systemInstruction,omitempty"`
	Contents          []Message          `json:"contents"`
	GenerationConfig  *GenerationConfig  `json:"generationConfig,omitempty"`
}

type ChatResponse struct {
	Candidates []Candidate `json:"candidates"`
}

type Candidate struct {
	Content Message `json:"content"`
}

func New() *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

// (pembaruan 3)
// (pembaruan 5)
func (c *Client) GenerateChat(apiKeys []string, systemPrompt string, history []Message, model string) (string, error) {
	var cleanKeys []string
	for _, k := range apiKeys {
		k = strings.ReplaceAll(k, "\r", "")
		subKeys := strings.Split(k, "\n")
		for _, sk := range subKeys {
			sk = strings.TrimSpace(sk)
			if sk != "" {
				cleanKeys = append(cleanKeys, sk)
			}
		}
	}

	if len(cleanKeys) == 0 {
		log.Println("Gemini API Error: No valid API keys provided after cleanup")
		return "", fmt.Errorf("no api keys provided by user")
	}
	apiKeys = cleanKeys

	if model == "" {
		model = "gemini-3.1-flash-lite-preview"
	}

	reqBody := ChatRequest{
		Contents: history,
	}

	if systemPrompt != "" {
		reqBody.SystemInstruction = &SystemInstruction{
			Parts: []Part{{Text: systemPrompt}},
		}
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		log.Printf("Failed to marshal Gemini request: %v\n", err)
		return "", err
	}

	maxRetries := len(apiKeys) * 3
	baseDelay := 2 * time.Second
	var lastErr error
	var bodyBytes []byte

	for attempt := 0; attempt <= maxRetries; attempt++ {
		currentKey := apiKeys[attempt%len(apiKeys)]
		url := fmt.Sprintf("https://generativelanguage.googleapis.com/v1beta/models/%s:generateContent", model)
		req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
		if err != nil {
			log.Printf("Failed to create Gemini request: %v\n", err)
			return "", err
		}

		req.Header.Set("x-goog-api-key", currentKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			log.Printf("Network error during Gemini request: %v\n", err)
			time.Sleep(baseDelay)
			continue
		}

		bodyBytes, err = io.ReadAll(resp.Body)
		resp.Body.Close()

		if err != nil {
			lastErr = err
			log.Printf("Failed to read Gemini response body: %v\n", err)
			continue
		}

		if resp.StatusCode == http.StatusOK {
			lastErr = nil
			break
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			lastErr = fmt.Errorf("Gemini API returned status %d", resp.StatusCode)
			log.Printf("Rate limit hit. Rotating key statically...\n")
			time.Sleep(baseDelay)
			continue
		}

		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("Gemini API returned status %d", resp.StatusCode)
			sleepDuration := time.Duration(1<<uint(attempt)) * baseDelay
			log.Printf("Server error %d. Retrying in %v...\n", resp.StatusCode, sleepDuration)
			time.Sleep(sleepDuration)
			continue
		}

		errMsg := fmt.Errorf("Gemini API returned fatal status %d: %s", resp.StatusCode, string(bodyBytes))
		log.Printf("Gemini API Fatal Error: %v\n", errMsg)
		return "", errMsg
	}

	if lastErr != nil {
		log.Printf("Gemini API Max Retries Reached. Last Error: %v\n", lastErr)
		return "", lastErr
	}

	var chatResp ChatResponse
	err = json.Unmarshal(bodyBytes, &chatResp)
	if err != nil {
		log.Printf("Failed to unmarshal Gemini response: %v\n", err)
		return "", err
	}

	if len(chatResp.Candidates) > 0 && len(chatResp.Candidates[0].Content.Parts) > 0 {
		return chatResp.Candidates[0].Content.Parts[0].Text, nil
	}

	log.Printf("Gemini API returned empty response: %s\n", string(bodyBytes))
	return "", fmt.Errorf("empty response from Gemini")
}