// (pembaruan 10) - File Baru: internal/tavily/client.go
package tavily

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

func New() *Client {
	return &Client{
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

func (c *Client) Search(apiKeys []string, query string) (string, error) {
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
		log.Println("Tavily API Error: No valid API keys provided")
		return "", fmt.Errorf("no api keys provided")
	}

	reqBody := map[string]interface{}{
		"query":          query,
		"max_results":    5,
		"search_depth":   "basic",
		"include_answer": false,
	}

	jsonData, err := json.Marshal(reqBody)
	if err != nil {
		log.Printf("Failed to marshal Tavily request: %v\n", err)
		return "", err
	}

	maxRetries := len(cleanKeys) * 3
	baseDelay := 1 * time.Second
	var lastErr error
	var bodyBytes []byte

	for attempt := 0; attempt <= maxRetries; attempt++ {
		currentKey := cleanKeys[attempt%len(cleanKeys)]
		req, err := http.NewRequest("POST", "https://api.tavily.com/search", bytes.NewBuffer(jsonData))
		if err != nil {
			log.Printf("Failed to create Tavily request: %v\n", err)
			return "", err
		}

		req.Header.Set("Authorization", "Bearer "+currentKey)
		req.Header.Set("Content-Type", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			lastErr = err
			log.Printf("Network error during Tavily request: %v\n", err)
			time.Sleep(baseDelay)
			continue
		}

		bodyBytes, err = io.ReadAll(resp.Body)
		resp.Body.Close()

		if err != nil {
			lastErr = err
			log.Printf("Failed to read Tavily response body: %v\n", err)
			continue
		}

		if resp.StatusCode == http.StatusOK {
			lastErr = nil
			break
		}

		lastErr = fmt.Errorf("Tavily API returned status %d: %s", resp.StatusCode, string(bodyBytes))
		log.Printf("Tavily API Error: %v\n", lastErr)
		time.Sleep(baseDelay)
	}

	if lastErr != nil {
		return "", lastErr
	}

	var searchResp struct {
		Results []struct {
			Title   string `json:"title"`
			Content string `json:"content"`
		} `json:"results"`
	}

	err = json.Unmarshal(bodyBytes, &searchResp)
	if err != nil {
		log.Printf("Failed to unmarshal Tavily response: %v\n", err)
		return "", err
	}

	if len(searchResp.Results) == 0 {
		return "No internet search results found for this query.", nil
	}

	var resultBuilder strings.Builder
	for i, res := range searchResp.Results {
		resultBuilder.WriteString(fmt.Sprintf("%d. %s\n%s\n\n", i+1, res.Title, res.Content))
	}

	return resultBuilder.String(), nil
}