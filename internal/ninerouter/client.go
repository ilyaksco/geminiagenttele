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

// --- TAMBAHKAN FUNGSI INI DI PALING BAWAH NINEROUTER.GO ---

// GenerateCopilotChat menghubungi server asli GitHub Copilot (Tanpa lewat 9Router)
func (c *Client) GenerateCopilotChat(prompt string, history []Message, model string, githubToken string) (string, error) {
	// 1. TUKAR TOKEN: Token biasa harus ditukar dengan Copilot Session Token
	reqToken, _ := http.NewRequest("GET", "https://api.github.com/copilot_internal/v2/token", nil)
	reqToken.Header.Set("Authorization", "Bearer "+githubToken)
	reqToken.Header.Set("User-Agent", "GitHubCopilotChat/0.11.1")
	reqToken.Header.Set("Accept", "application/json")

	respToken, err := c.HttpClient.Do(reqToken)
	if err != nil {
		return "", fmt.Errorf("gagal menghubungi GitHub: %v", err)
	}
	defer respToken.Body.Close()

	if respToken.StatusCode != 200 {
		return "", fmt.Errorf("akun Anda tidak memiliki langganan GitHub Copilot yang aktif")
	}

	var tokenData struct {
		Token string `json:"token"`
	}
	json.NewDecoder(respToken.Body).Decode(&tokenData)
	copilotSessionToken := tokenData.Token

	// 2. KIRIM CHAT: Kirim pesan langsung ke Server Asli GitHub Copilot
	messages := []Message{{Role: "system", Content: prompt}}
	messages = append(messages, history...)

	reqBody := map[string]interface{}{
		"model":    model,
		"messages": messages,
	}
	jsonData, _ := json.Marshal(reqBody)

	req, _ := http.NewRequest("POST", "https://api.githubcopilot.com/chat/completions", bytes.NewBuffer(jsonData))
	
	// Gunakan token session yang baru saja ditukar
	req.Header.Set("Authorization", "Bearer "+copilotSessionToken)
	req.Header.Set("Content-Type", "application/json")
	
	// Header sakti agar dianggap sebagai aplikasi resmi (Bypass blokir)
	req.Header.Set("Editor-Version", "vscode/1.85.0")
	req.Header.Set("Editor-Plugin-Version", "copilot-chat/0.11.1")
	req.Header.Set("User-Agent", "GitHubCopilotChat/0.11.1")

	log.Printf("⏳ Mengirim chat LANGSUNG ke api.githubcopilot.com dengan model: %s...\n", model)

	resp, err := c.HttpClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	body, _ := io.ReadAll(resp.Body)
	rawString := string(body)

	if resp.StatusCode != 200 {
		log.Printf("❌ Copilot Direct Error (Status %d): %s\n", resp.StatusCode, rawString)
		return "", fmt.Errorf("api copilot menolak permintaan (status %d)", resp.StatusCode)
	}

	var result struct {
		Choices []struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		} `json:"choices"`
	}

	if err := json.Unmarshal([]byte(rawString), &result); err != nil {
		return "", err
	}

	if len(result.Choices) > 0 {
		return result.Choices[0].Message.Content, nil
	}

	return "", fmt.Errorf("copilot tidak memberikan jawaban")
}