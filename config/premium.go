package config

import (
	"encoding/json"
	"log"
	"os"
)

type PremiumConfig struct {
	MaxFreeBots         int `json:"max_free_bots"`
	PremiumPriceStars   int `json:"premium_price_stars"`
	PremiumDurationDays int `json:"premium_duration_days"`
}

func LoadPremium() *PremiumConfig {
	file, err := os.ReadFile("premium.json")
	if err != nil {
		log.Println("premium.json not found, using default fallback...")
		return &PremiumConfig{MaxFreeBots: 5, PremiumPriceStars: 50, PremiumDurationDays: 30}
	}

	var cfg PremiumConfig
	if err := json.Unmarshal(file, &cfg); err != nil {
		log.Fatalf("Failed to parse premium.json: %v", err)
	}
	return &cfg
}