package config

import (
	"bufio"
	"os"
	"strings"
)

type Config struct {
	TelegramToken string
	GroqAPIKeys   []string
	DatabaseURL   string
}

func Load() *Config {
	file, err := os.Open(".env")
	if err == nil {
		defer file.Close()
		scanner := bufio.NewScanner(file)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if len(line) == 0 {
				continue
			}
			parts := strings.SplitN(line, "=", 2)
			if len(parts) == 2 {
				os.Setenv(parts[0], parts[1])
			}
		}
	}

	rawKeys := strings.Split(os.Getenv("GROQ_API_KEYS"), ",")
	var apiKeys []string
	for _, k := range rawKeys {
		cleaned := strings.TrimSpace(k)
		if cleaned != "" {
			apiKeys = append(apiKeys, cleaned)
		}
	}

	return &Config{
		TelegramToken: os.Getenv("TELEGRAM_TOKEN"),
		GroqAPIKeys:   apiKeys,
		DatabaseURL:   os.Getenv("DATABASE_URL"),
	}
}