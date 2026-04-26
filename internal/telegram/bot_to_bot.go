package telegram

import (
	"log"
	"sync"
	"time"
)

type BotTracker struct {
	mu           sync.Mutex
	lastActivity map[int64]map[int64]time.Time
	chainCount   map[int64]int
}

func NewBotTracker() *BotTracker {
	return &BotTracker{
		lastActivity: make(map[int64]map[int64]time.Time),
		chainCount:   make(map[int64]int),
	}
}

func (bt *BotTracker) AllowBotInteraction(chatID int64, senderID int64) bool {
	bt.mu.Lock()
	defer bt.mu.Unlock()

	now := time.Now()

	if _, exists := bt.lastActivity[chatID]; !exists {
		bt.lastActivity[chatID] = make(map[int64]time.Time)
	}

	if lastTime, exists := bt.lastActivity[chatID][senderID]; exists {
		if now.Sub(lastTime) < 5*time.Second {
			log.Printf("Loop prevention triggered: rate limit for bot %d in chat %d", senderID, chatID)
			return false
		}
	}

	if bt.chainCount[chatID] > 10 {
		if now.Sub(bt.lastActivity[chatID][senderID]) < 30*time.Second {
			log.Printf("Loop prevention triggered: max depth chain reached in chat %d", chatID)
			return false
		}
		bt.chainCount[chatID] = 0
	}

	bt.lastActivity[chatID][senderID] = now
	bt.chainCount[chatID]++

	return true
}

func (bt *BotTracker) ResetChain(chatID int64) {
	bt.mu.Lock()
	defer bt.mu.Unlock()
	bt.chainCount[chatID] = 0
}