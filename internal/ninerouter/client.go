package ninerouter

import (
	"bytes"
	"encoding/json"
	"strings" // TAMBAHKAN BARIS INI
	"fmt"
	"io"
	"log"
	"net/http"
	"time"
)

type Client struct {
	BaseURL    string
	HttpClient *http.Client
}

type Message struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

func NewClient() *Client {
	return &Client{
		// Gunakan 127.0.0.1 untuk menghindari bug localhost di Windows
		BaseURL: "http://127.0.0.1:20128/v1/chat/completions", 
		// Tingkatkan waktu tunggu menjadi 120 detik (OpenCode kadang antre)
		HttpClient: &http.Client{Timeout: 120 * time.Second},
	}
}

func (c *Client) GenerateChat(prompt string, history []Message, model string, apiKey string) (string, error) {
	messages := []Message{{Role: "system", Content: prompt}}
	messages = append(messages, history...)

	reqBody := map[string]interface{}{
		"model":    model,
		"messages": messages,
	}

	jsonData, _ := json.Marshal(reqBody)
	req, _ := http.NewRequest("POST", c.BaseURL, bytes.NewBuffer(jsonData))

	if apiKey == "" {
		apiKey = "sk_9router"
	}
	
	// PERBAIKAN: Gunakan API Key asli dari 9Router!
	req.Header.Set("Authorization", "Bearer sk_9router"+apiKey)
	req.Header.Set("Content-Type", "application/json")

	// LOG: Cetak info bahwa bot sedang mengirim permintaan ke 9Router
	log.Printf("⏳ Mengirim permintaan ke 9Router dengan model: %s...\n", model)

	resp, err := c.HttpClient.Do(req)
	if err != nil {
		log.Printf("❌ Gagal menghubungi server 9Router: %v\n", err)
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	rawString := string(body)

	// LOG: Cetak balasan MENTAH dari 9Router ke terminal

	// --- PERBAIKAN BUG: Bersihkan teks sampah dari 9Router ---
	rawString = strings.ReplaceAll(rawString, "data: [DONE]", "")
	rawString = strings.TrimSpace(rawString)
	// ---------------------------------------------------------

	if resp.StatusCode != 200 {
		log.Printf("❌ 9Router Error (Status %d): %s\n", resp.StatusCode, rawString)
		return "", fmt.Errorf("9Router error: status %d", resp.StatusCode)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	// Gunakan rawString yang sudah dibersihkan untuk diurai (Unmarshal)
	if err := json.Unmarshal([]byte(rawString), &result); err != nil {
		log.Printf("❌ Gagal membaca format JSON dari 9Router: %v\n", err)
		return "", err
	}

	if len(result.Choices) > 0 {
		return result.Choices[0].Message.Content, nil
	}

	return "", fmt.Errorf("9Router tidak memberikan jawaban (kosong)")
}