package database

import (
	"database/sql"
	"log"
	"time"

	_ "modernc.org/sqlite"
)

type DB struct {
	Conn *sql.DB
}

type ChatMessage struct {
	Role    string
	Content string
}

type ManagedBot struct {
	BotID    int64
	OwnerID  int64
	Username string
	Name        string
	Token    string
	SystemPrompt string
	Model        string
}

func New(dbURL string) *DB {
	conn, err := sql.Open("sqlite", dbURL)
	if err != nil {
		log.Fatalf("Failed to open database: %v\n", err)
	}

	err = conn.Ping()
	if err != nil {
		log.Fatalf("Failed to ping database: %v\n", err)
	}

	_, _ = conn.Exec("PRAGMA journal_mode=WAL;")
	_, _ = conn.Exec("PRAGMA synchronous=NORMAL;")
	_, _ = conn.Exec("PRAGMA cache_size=-2000;")
	_, _ = conn.Exec("PRAGMA busy_timeout=5000;")

	queries := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY,
			language TEXT NOT NULL DEFAULT 'en',
			encrypted_api_keys TEXT DEFAULT ''
		);`,
		`CREATE TABLE IF NOT EXISTS chat_history (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			bot_id INTEGER NOT NULL,
			chat_id INTEGER NOT NULL,
			thread_id INTEGER NOT NULL,
			role TEXT NOT NULL,
			content TEXT NOT NULL,
			created_at DATETIME DEFAULT CURRENT_TIMESTAMP
		);`,
		`CREATE TABLE IF NOT EXISTS managed_bots (
			bot_id INTEGER PRIMARY KEY,
			owner_id INTEGER NOT NULL DEFAULT 0,
			username TEXT NOT NULL DEFAULT '',
			name TEXT NOT NULL DEFAULT '',
			token TEXT NOT NULL,
			system_prompt TEXT DEFAULT ''
		);`,
	}

	for _, q := range queries {
		_, _ = conn.Exec(q)
	}

	_, _ = conn.Exec(`ALTER TABLE managed_bots ADD COLUMN name TEXT DEFAULT '';`)
	_, _ = conn.Exec(`ALTER TABLE managed_bots ADD COLUMN username TEXT DEFAULT '';`)
	_, _ = conn.Exec(`ALTER TABLE chat_history ADD COLUMN bot_id INTEGER DEFAULT 0;`)
	_, _ = conn.Exec(`ALTER TABLE managed_bots ADD COLUMN owner_id INTEGER DEFAULT 0;`)
	_, _ = conn.Exec(`ALTER TABLE users ADD COLUMN encrypted_api_keys TEXT DEFAULT '';`)
	_, _ = conn.Exec(`ALTER TABLE users ADD COLUMN encrypted_gemini_keys TEXT DEFAULT '';`)
	_, _ = conn.Exec(`ALTER TABLE users ADD COLUMN premium_until DATETIME;`)
	_, _ = conn.Exec(`ALTER TABLE managed_bots ADD COLUMN model TEXT DEFAULT 'openai/gpt-oss-120b';`)

	log.Println("Database connection established and tables verified")
	return &DB{Conn: conn}
}

func (db *DB) SetUserGeminiKeys(userID int64, encryptedKeys string) {
	query := `UPDATE users SET encrypted_gemini_keys = ? WHERE id = ?;`
	_, err := db.Conn.Exec(query, encryptedKeys, userID)
	if err != nil {
		log.Printf("Failed to save user Gemini keys: %v\n", err)
	}
}

func (db *DB) GetUserGeminiKeys(userID int64) string {
	var keys sql.NullString
	query := `SELECT encrypted_gemini_keys FROM users WHERE id = ?;`
	err := db.Conn.QueryRow(query, userID).Scan(&keys)
	if err != nil {
		return ""
	}
	if keys.Valid {
		return keys.String
	}
	return ""
}


func (db *DB) CountUserBots(ownerID int64) int {
	var count int
	query := `SELECT COUNT(*) FROM managed_bots WHERE owner_id = ?;`
	err := db.Conn.QueryRow(query, ownerID).Scan(&count)
	if err != nil {
		return 0
	}
	return count
}

func (db *DB) IsUserPremium(ownerID int64) bool {
	var premiumUntil sql.NullTime
	query := `SELECT premium_until FROM users WHERE id = ?;`
	err := db.Conn.QueryRow(query, ownerID).Scan(&premiumUntil)
	if err != nil || !premiumUntil.Valid {
		return false
	}
	return premiumUntil.Time.After(time.Now())
}

func (db *DB) GrantPremium(ownerID int64, days int) {
	query := `INSERT INTO users (id, premium_until) VALUES (?, datetime('now', '+' || ? || ' days')) ON CONFLICT(id) DO UPDATE SET premium_until = datetime('now', '+' || ? || ' days');`
	_, err := db.Conn.Exec(query, ownerID, days, days)
	if err != nil {
		log.Printf("Failed to grant premium: %v\n", err)
	}
}

func (db *DB) SetUserAPIKeys(userID int64, encryptedKeys string) {
	query := `INSERT INTO users (id, encrypted_api_keys) VALUES (?, ?) ON CONFLICT(id) DO UPDATE SET encrypted_api_keys = ?;`
	_, err := db.Conn.Exec(query, userID, encryptedKeys, encryptedKeys)
	if err != nil {
		log.Printf("Failed to save user API keys: %v\n", err)
	}
}

func (db *DB) GetUserAPIKeys(userID int64) string {
	var keys sql.NullString
	query := `SELECT encrypted_api_keys FROM users WHERE id = ?;`
	err := db.Conn.QueryRow(query, userID).Scan(&keys)
	if err != nil {
		return ""
	}
	if keys.Valid {
		return keys.String
	}
	return ""
}

// --- TIMPA FUNGSI LAMA DENGAN KETIGA FUNGSI INI DI internal/database/sqlite.go ---

func (db *DB) SaveMessage(botID int64, chatID int64, threadID int, role string, content string) {
	// Sekarang bot_id benar-benar dimasukkan ke dalam database!
	query := `INSERT INTO chat_history (bot_id, chat_id, thread_id, role, content) VALUES (?, ?, ?, ?, ?);`
	_, err := db.Conn.Exec(query, botID, chatID, threadID, role, content)
	if err != nil {
		log.Printf("Failed to save message: %v\n", err)
	}
}

// --- TIMPA FUNGSI GetHistory DI internal/database/sqlite.go DENGAN INI ---

func (db *DB) GetHistory(botID int64, chatID int64, threadID int, limit int) []ChatMessage {
	var history []ChatMessage
	
	// Sekarang bot hanya membaca ingatan miliknya sendiri (termasuk legacy bot_id = 0)
	query := `
		SELECT role, content FROM (
			SELECT role, content, id FROM chat_history 
			WHERE chat_id = ? AND thread_id = ? AND (bot_id = ? OR bot_id = 0)
			ORDER BY id DESC LIMIT ?
		) ORDER BY id ASC;
	`
	rows, err := db.Conn.Query(query, chatID, threadID, botID, limit)
	if err != nil {
		return history
	}
	defer rows.Close()

	for rows.Next() {
		var role, content string
		if err := rows.Scan(&role, &content); err == nil {
			history = append(history, ChatMessage{Role: role, Content: content})
		}
	}
	return history
}

func (db *DB) ClearChatHistory(botID int64, chatID int64, threadID int) error {
	// Menghapus ingatan bot tersebut, sekaligus membersihkan data bug lama (bot_id = 0)
	query := `DELETE FROM chat_history WHERE chat_id = ? AND thread_id = ? AND (bot_id = ? OR bot_id = 0);`
	_, err := db.Conn.Exec(query, chatID, threadID, botID)
	return err
}

func (db *DB) SaveManagedBot(botID int64, ownerID int64, username string, name string, token string) {
	query := `INSERT INTO managed_bots (bot_id, owner_id, username, name, token, model) VALUES (?, ?, ?, ?, ?, 'openai/gpt-oss-120b') ON CONFLICT(bot_id) DO UPDATE SET token = ?, username = ?, name = ?, owner_id = ?;`
	_, err := db.Conn.Exec(query, botID, ownerID, username, name, token, token, username, name, ownerID)
	if err != nil {
		log.Printf("Failed to save managed bot: %v\n", err)
	}
}

func (db *DB) GetManagedBot(botID int64) *ManagedBot {
	var bot ManagedBot
	var prompt, model sql.NullString
	
	query := `SELECT bot_id, owner_id, username, name, token, system_prompt, model FROM managed_bots WHERE bot_id = ?;`
	err := db.Conn.QueryRow(query, botID).Scan(&bot.BotID, &bot.OwnerID, &bot.Username, &bot.Name, &bot.Token, &prompt, &model)
	if err != nil {
		return nil
	}
	
	if prompt.Valid {
		bot.SystemPrompt = prompt.String
	} else {
		bot.SystemPrompt = ""
	}

	if model.Valid && model.String != "" {
		bot.Model = model.String
	} else {
		bot.Model = "openai/gpt-oss-120b"
	}
	
	return &bot
}

func (db *DB) SetBotModel(botID int64, model string) bool {
	query := `UPDATE managed_bots SET model = ? WHERE bot_id = ?;`
	res, err := db.Conn.Exec(query, model, botID)
	if err != nil {
		log.Printf("Failed to set bot model: %v\n", err)
		return false
	}
	rows, _ := res.RowsAffected()
	return rows > 0
}

func (db *DB) DeleteManagedBot(botID int64, ownerID int64) bool {
	query := `DELETE FROM managed_bots WHERE bot_id = ? AND owner_id = ?;`
	res, err := db.Conn.Exec(query, botID, ownerID)
	if err != nil {
		log.Printf("Failed to delete bot: %v\n", err)
		return false
	}
	rows, _ := res.RowsAffected()
	return rows > 0
}

func (db *DB) GetBotsByOwner(ownerID int64) []ManagedBot {
	query := `SELECT bot_id, name FROM managed_bots WHERE owner_id = ?;`
	rows, err := db.Conn.Query(query, ownerID)
	if err != nil {
		log.Printf("Failed to fetch user bots: %v\n", err)
		return nil
	}
	defer rows.Close()

	var bots []ManagedBot
	for rows.Next() {
		var bot ManagedBot
		if err := rows.Scan(&bot.BotID, &bot.Name); err != nil {
			continue
		}
		bots = append(bots, bot)
	}
	return bots
}

func (db *DB) GetBotOwner(botID int64) int64 {
	var ownerID int64
	query := `SELECT owner_id FROM managed_bots WHERE bot_id = ?;`
	err := db.Conn.QueryRow(query, botID).Scan(&ownerID)
	if err != nil {
		return 0
	}
	return ownerID
}

func (db *DB) SetBotPrompt(botID int64, prompt string) bool {
	query := `UPDATE managed_bots SET system_prompt = ? WHERE bot_id = ?;`
	res, err := db.Conn.Exec(query, prompt, botID)
	if err != nil {
		log.Printf("Failed to set bot prompt: %v\n", err)
		return false
	}
	rows, _ := res.RowsAffected()
	return rows > 0
}

func (db *DB) GetBotPrompt(botID int64) string {
	var prompt sql.NullString
	query := `SELECT system_prompt FROM managed_bots WHERE bot_id = ?;`
	err := db.Conn.QueryRow(query, botID).Scan(&prompt)
	if err != nil {
		return ""
	}
	if prompt.Valid {
		return prompt.String
	}
	return ""
}

func (db *DB) SetUserLang(userID int64, lang string) error {
	query := `INSERT INTO users (id, language) VALUES (?, ?) ON CONFLICT(id) DO UPDATE SET language = ?;`
	_, err := db.Conn.Exec(query, userID, lang, lang)
	if err != nil {
		log.Printf("Error setting user language: %v\n", err)
	}
	return err
}

func (db *DB) GetUserLang(userID int64) string {
	var lang string
	query := `SELECT language FROM users WHERE id = ?;`
	err := db.Conn.QueryRow(query, userID).Scan(&lang)
	if err != nil {
		if err == sql.ErrNoRows {
			return "en"
		}
		log.Printf("Error getting user language: %v\n", err)
		return "en"
	}
	return lang
}

func (db *DB) SetBotPromptByOwner(ownerID int64, username string, prompt string) bool {
	query := `UPDATE managed_bots SET system_prompt = ? WHERE LOWER(username) = LOWER(?) AND owner_id = ?;`
	res, err := db.Conn.Exec(query, prompt, username, ownerID)
	if err != nil {
		log.Printf("Failed to set bot prompt: %v\n", err)
		return false
	}
	rows, _ := res.RowsAffected()
	return rows > 0
}

func (db *DB) GetManagedBots() []ManagedBot {
	query := `SELECT bot_id, owner_id, username, name, token, system_prompt FROM managed_bots;`
	rows, err := db.Conn.Query(query)
	if err != nil {
		log.Printf("Failed to fetch all managed bots: %v\n", err)
		return nil
	}
	defer rows.Close()

	var bots []ManagedBot
	for rows.Next() {
		var bot ManagedBot
		var prompt sql.NullString
		
		if err := rows.Scan(&bot.BotID, &bot.OwnerID, &bot.Username, &bot.Name, &bot.Token, &prompt); err == nil {
			if prompt.Valid {
				bot.SystemPrompt = prompt.String
			}
			bots = append(bots, bot)
		} else {
			log.Printf("Failed to scan bot data: %v\n", err)
		}
	}
	return bots
}

// --- TAMBAHKAN DI BAGIAN PALING BAWAH FILE internal/database/sqlite.go ---

