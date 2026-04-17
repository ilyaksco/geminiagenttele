package main

import (
	"gemini-agent/config"
	"gemini-agent/internal/database"
	"gemini-agent/internal/groq"
	"gemini-agent/internal/i18n"
	"gemini-agent/internal/telegram"
	"log"
	"os"
	"os/signal"
	"syscall"
	"time"
)

var (
	db         *database.DB
	groqClient *groq.Client
	i18nSys    *i18n.I18n
	cfg        *config.Config
)

func main() {
	log.Println("Starting Public Bot Factory Backend with Groq API Rotation...")

	cfg = config.Load()
	if cfg.TelegramToken == "" || len(cfg.GroqAPIKeys) == 0 {
		log.Fatalf("Missing critical environment variables (Token or API Keys)")
	}

	i18nSys = i18n.New()
	db = database.New(cfg.DatabaseURL)
	groqClient = groq.New(cfg.GroqAPIKeys)

	startBotInstance(cfg.TelegramToken, true)

	managedBots := db.GetManagedBots()
	log.Printf("Found %d managed bots in database. Booting them up...\n", len(managedBots))
	for _, bot := range managedBots {
		startBotInstance(bot.Token, false)
	}

	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)
	<-quit

	log.Println("Graceful shutdown initiated...")
	db.Conn.Close()
	log.Println("All systems offline.")
}

func startBotInstance(token string, isManager bool) {
	go func() {
		tgClient := telegram.NewClient(token)
		botUser, err := tgClient.GetMe()
		if err != nil {
			log.Printf("Failed to start bot instance (isManager: %v): %v\n", isManager, err)
			return
		}

		role := "Managed Clone (AI Active)"
		if isManager {
			role = "Manager Bot (Lobotomized)"
		}
		log.Printf("[%s] Authorized successfully as @%s (ID: %d)\n", role, botUser.Username, botUser.ID)

		onNewBotSpawned := func(botID int64, newToken string) {
			log.Printf("Manager detected new clone creation! Spawning instance dynamically...\n")
			startBotInstance(newToken, false)
		}

		handler := telegram.NewHandler(tgClient, db, groqClient, i18nSys, botUser, isManager, onNewBotSpawned)

		offset := 0
		for {
			updates, err := tgClient.GetUpdates(offset)
			if err != nil {
				time.Sleep(2 * time.Second)
				continue
			}

			for _, u := range updates {
				if u.UpdateID >= offset {
					offset = u.UpdateID + 1
				}
				handler.HandleUpdate(u)
			}
		}
	}()
}
