package config

import (
	"bufio"
	"os"
	"strings"
)

type Config struct {
	TelegramToken string
	EncryptionKey string
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

	keyBytes := []byte(os.Getenv("ENCRYPTION_KEY"))
	if len(keyBytes) < 32 {
		padded := make([]byte, 32)
		copy(padded, keyBytes)
		keyBytes = padded
	} else if len(keyBytes) > 32 {
		keyBytes = keyBytes[:32]
	}

	return &Config{
		TelegramToken: os.Getenv("TELEGRAM_TOKEN"),
		EncryptionKey: string(keyBytes),
		DatabaseURL:   os.Getenv("DATABASE_URL"),
	}
}