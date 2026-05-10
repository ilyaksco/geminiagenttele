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

const (
	githubClientID      = "01ab8ac9400c4e429b23" // PASTIKAN CLIENT ID ANDA SUDAH BENAR DI SINI
	githubDeviceCodeURL = "https://github.com/login/device/code"
	githubTokenURL      = "https://github.com/login/oauth/access_token"
)

type DeviceCodeResponse struct {
	DeviceCode      string `json:"device_code"`
	UserCode        string `json:"user_code"`
	VerificationURI string `json:"verification_uri"`
	ExpiresIn       int    `json:"expires_in"`
	Interval        int    `json:"interval"`
}

type AccessTokenResponse struct {
	AccessToken string `json:"access_token"`
	Error       string `json:"error"`
	Interval    int    `json:"interval"`
}

// PERUBAHAN: Menambahkan msgID int ke dalam parameter
func (h *Handler) StartGitHubOAuth(chatID int64, msgID int, userID int64, lang string) {
	log.Printf("[GITHUB OAUTH] Memulai permintaan login untuk UserID: %d\n", userID)
	client := &http.Client{Timeout: 10 * time.Second}

	payload, _ := json.Marshal(map[string]string{
		"client_id": githubClientID,
		"scope":     "read:user",
	})

	req, _ := http.NewRequest("POST", githubDeviceCodeURL, bytes.NewBuffer(payload))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := client.Do(req)
	if err != nil {
		log.Printf("[GITHUB OAUTH] Error HTTP Request: %v\n", err)
		h.editMsg(chatID, msgID, h.i18n.Get(lang, "oauth_github_error"), true, nil)
		return
	}
	defer resp.Body.Close()

	var deviceData DeviceCodeResponse
	json.NewDecoder(resp.Body).Decode(&deviceData)

	if deviceData.UserCode == "" {
		h.editMsg(chatID, msgID, "❌ Error: Client ID tidak valid atau ditolak GitHub.", true, nil)
		return
	}

	log.Printf("[GITHUB OAUTH] Berhasil dapat kode: %s. Mulai polling...\n", deviceData.UserCode)
	text := fmt.Sprintf(h.i18n.Get(lang, "oauth_github_instruction"), deviceData.VerificationURI, deviceData.UserCode)
	
	// PERUBAHAN: Bot mengedit menu langsung menjadi instruksi login
	h.editMsg(chatID, msgID, text, true, nil)

	// Teruskan msgID ke pekerja latar belakang
	go h.pollGitHubToken(chatID, msgID, userID, deviceData, lang)
}

// PERUBAHAN: Menambahkan msgID int ke dalam parameter
func (h *Handler) pollGitHubToken(chatID int64, msgID int, userID int64, data DeviceCodeResponse, lang string) {
	client := &http.Client{Timeout: 10 * time.Second}
	interval := time.Duration(data.Interval) * time.Second
	if interval == 0 {
		interval = 5 * time.Second
	}

	expiresAt := time.Now().Add(time.Duration(data.ExpiresIn) * time.Second)

	for time.Now().Before(expiresAt) {
		time.Sleep(interval) 

		log.Printf("[GITHUB OAUTH] Mengecek status ke GitHub... (Interval saat ini: %v)", interval)

		payload, _ := json.Marshal(map[string]string{
			"client_id":   githubClientID,
			"device_code": data.DeviceCode,
			"grant_type":  "urn:ietf:params:oauth:grant-type:device_code",
		})

		req, _ := http.NewRequest("POST", githubTokenURL, bytes.NewBuffer(payload))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Accept", "application/json")

		resp, err := client.Do(req)
		if err != nil {
			log.Printf("[GITHUB OAUTH] Error Koneksi: %v\n", err)
			continue
		}

		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		var tokenData AccessTokenResponse
		json.Unmarshal(body, &tokenData)

		if tokenData.AccessToken != "" {
			log.Printf("[GITHUB OAUTH] SUKSES! Token GitHub didapatkan!\n")
			h.db.SetGitHubKey(userID, tokenData.AccessToken)
			
			// PERUBAHAN: Saat sukses, bot MENGEDIT pesan instruksi menjadi pesan sukses!
			h.editMsg(chatID, msgID, h.i18n.Get(lang, "oauth_github_success"), true, nil)
			return
		}

		if tokenData.Error == "authorization_pending" {
			continue 
		} else if tokenData.Error == "slow_down" {
			if tokenData.Interval > 0 {
				interval = time.Duration(tokenData.Interval) * time.Second
			} else {
				interval += 5 * time.Second
			}
			log.Printf("[GITHUB OAUTH] Diperingatkan (Slow Down). Interval diubah jadi: %v\n", interval)
		} else {
			log.Printf("[GITHUB OAUTH] BERHENTI. Error fatal: %s\n", tokenData.Error)
			h.editMsg(chatID, msgID, "❌ Otorisasi ditolak GitHub: "+tokenData.Error, true, nil)
			return
		}
	}

	// Jika waktu habis, edit pesan instruksi jadi timeout
	log.Printf("[GITHUB OAUTH] Waktu habis untuk UserID: %d\n", userID)
	h.editMsg(chatID, msgID, h.i18n.Get(lang, "oauth_github_timeout"), true, nil)
}