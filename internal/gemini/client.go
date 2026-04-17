package gemini

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync"
	"time"
)

type Client struct {
	apiKeys    []string
	keyIndex   int
	mu         sync.Mutex
	httpClient *http.Client
}

type GenerateRequest struct {
	Contents []Content `json:"contents"`
}

type Content struct {
	Role  string `json:"role,omitempty"`
	Parts []Part `json:"parts"`
}

type Part struct {
	Text string `json:"text"`
}

type GenerateResponse struct {
	Candidates []Candidate `json:"candidates"`
}

type Candidate struct {
	Content Content `json:"content"`
}

func New(apiKeys []string) *Client {
	if len(apiKeys) == 0 {
		log.Fatalf("No Gemini API keys provided")
	}
	return &Client{
		apiKeys:    apiKeys,
		keyIndex:   0,
		httpClient: &http.Client{Timeout: 60 * time.Second},
	}
}

func (c *Client) getKey() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.apiKeys[c.keyIndex%len(c.apiKeys)]
}

func (c *Client) rotateKey() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.keyIndex++
	log.Printf("Rotated to API key index %d due to rate limit\n", c.keyIndex%len(c.apiKeys))
}

func (c *Client) GenerateChat(history []Content) (string, error) {
	url := "https://generativelanguage.googleapis.com/v1beta/models/gemini-3.1-flash-lite-preview:generateContent"

	reqBody := GenerateRequest{
		Contents: history,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		log.Printf("Failed to marshal Gemini request: %v\n", err)
		return "", err
	}

	maxRetries := len(c.apiKeys) * 3
	baseDelay := 2 * time.Second
	var lastErr error
	var bodyBytes []byte

	for attempt := 0; attempt <= maxRetries; attempt++ {
		currentKey := c.getKey()

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

		if resp.StatusCode == http.StatusTooManyRequests || resp.StatusCode == http.StatusServiceUnavailable {
			lastErr = fmt.Errorf("Gemini API returned status %d: %s", resp.StatusCode, string(bodyBytes))
			log.Printf("Rate limit hit or Server Busy. Initiating key rotation...\n")
			c.rotateKey()
			time.Sleep(baseDelay)
			continue
		}

		if resp.StatusCode >= 500 {
			lastErr = fmt.Errorf("Gemini API returned status %d: %s", resp.StatusCode, string(bodyBytes))
			sleepDuration := time.Duration(1<<uint(attempt)) * baseDelay
			log.Printf("Server error %d. Retrying in %v...\n", resp.StatusCode, sleepDuration)
			time.Sleep(sleepDuration)
			continue
		}

		return "", fmt.Errorf("Gemini API returned fatal status %d: %s", resp.StatusCode, string(bodyBytes))
	}

	if lastErr != nil {
		return "", lastErr
	}

	var genResp GenerateResponse
	err = json.Unmarshal(bodyBytes, &genResp)
	if err != nil {
		log.Printf("Failed to unmarshal Gemini response: %v\n", err)
		return "", err
	}

	if len(genResp.Candidates) > 0 && len(genResp.Candidates[0].Content.Parts) > 0 {
		return genResp.Candidates[0].Content.Parts[0].Text, nil
	}

	return "", fmt.Errorf("empty response from Gemini")
}