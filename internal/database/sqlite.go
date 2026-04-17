package database

import (
	"database/sql"
	"log"

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
	Token    string
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

	queries := []string{
		`CREATE TABLE IF NOT EXISTS users (
			id INTEGER PRIMARY KEY,
			language TEXT NOT NULL DEFAULT 'en'
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
			token TEXT NOT NULL,
			system_prompt TEXT DEFAULT ''
		);`,
	}

	for _, q := range queries {
		_, err = conn.Exec(q)
		if err != nil && err.Error() != "table chat_history already exists" && err.Error() != "table managed_bots already exists" {
			log.Printf("Init query skipped/handled: %v\n", err)
		}
	}

	_, _ = conn.Exec(`ALTER TABLE managed_bots ADD COLUMN username TEXT DEFAULT '';`)
	_, _ = conn.Exec(`ALTER TABLE chat_history ADD COLUMN bot_id INTEGER DEFAULT 0;`)
	_, _ = conn.Exec(`ALTER TABLE managed_bots ADD COLUMN owner_id INTEGER DEFAULT 0;`)

	log.Println("Database connection established and tables verified")
	return &DB{Conn: conn}
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

func (db *DB) SaveMessage(botID int64, chatID int64, threadID int, role string, content string) {
	query := `INSERT INTO chat_history (bot_id, chat_id, thread_id, role, content) VALUES (?, ?, ?, ?, ?)`
	_, err := db.Conn.Exec(query, botID, chatID, threadID, role, content)
	if err != nil {
		log.Printf("Failed to save chat history: %v\n", err)
	}
}

func (db *DB) GetHistory(botID int64, chatID int64, threadID int, limit int) []ChatMessage {
	query := `
	SELECT role, content FROM (
		SELECT id, role, content FROM chat_history 
		WHERE bot_id = ? AND chat_id = ? AND thread_id = ? 
		ORDER BY id DESC LIMIT ?
	) ORDER BY id ASC;`

	rows, err := db.Conn.Query(query, botID, chatID, threadID, limit)
	if err != nil {
		log.Printf("Failed to fetch chat history: %v\n", err)
		return nil
	}
	defer rows.Close()

	var history []ChatMessage
	for rows.Next() {
		var msg ChatMessage
		if err := rows.Scan(&msg.Role, &msg.Content); err != nil {
			log.Printf("Failed to scan history row: %v\n", err)
			continue
		}
		history = append(history, msg)
	}
	return history
}

func (db *DB) SaveManagedBot(botID int64, ownerID int64, username string, token string) {
	query := `INSERT INTO managed_bots (bot_id, owner_id, username, token) VALUES (?, ?, ?, ?) ON CONFLICT(bot_id) DO UPDATE SET token = ?, username = ?, owner_id = ?;`
	_, err := db.Conn.Exec(query, botID, ownerID, username, token, token, username, ownerID)
	if err != nil {
		log.Printf("Failed to save managed bot: %v\n", err)
	}
}

func (db *DB) GetManagedBots() []ManagedBot {
	query := `SELECT bot_id, owner_id, username, token FROM managed_bots;`
	rows, err := db.Conn.Query(query)
	if err != nil {
		log.Printf("Failed to fetch managed bots: %v\n", err)
		return nil
	}
	defer rows.Close()

	var bots []ManagedBot
	for rows.Next() {
		var bot ManagedBot
		if err := rows.Scan(&bot.BotID, &bot.OwnerID, &bot.Username, &bot.Token); err != nil {
			continue
		}
		bots = append(bots, bot)
	}
	return bots
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

func (db *DB) GetBotPrompt(botID int64) string {
	var prompt sql.NullString
	query := `SELECT system_prompt FROM managed_bots WHERE bot_id = ?;`
	err := db.Conn.QueryRow(query, botID).Scan(&prompt)
	if err != nil {
		if err != sql.ErrNoRows {
			log.Printf("Error getting bot prompt: %v\n", err)
		}
		return ""
	}
	if prompt.Valid {
		return prompt.String
	}
	return ""
}