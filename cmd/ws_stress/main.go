package main

import (
	"encoding/json"
	"fmt"
	"log"
	"math/rand"
	"os"
	"sync"
	"time"

	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
)

const (
	clientCount = 1000
)

func main() {
	err := godotenv.Load()

	if err != nil {
		panic(err)
	}

	var wg sync.WaitGroup

	for i := 0; i < clientCount; i++ {
		wg.Add(1)
		go func(clientID int) {
			defer wg.Done()
			if err := runClient(clientID); err != nil {
				log.Printf("Client %d error: %v\n", clientID, err)
			}
		}(i)
	}

	wg.Wait()
	fmt.Println("Stress test completed.")
}

const charset = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

func randomString(minLen, maxLen int) string {
	length := rand.Intn(maxLen-minLen+1) + minLen

	result := make([]byte, length)
	for i := range result {
		result[i] = charset[rand.Intn(len(charset))]
	}
	return string(result)
}

func runClient(clientID int) error {
	u := fmt.Sprintf("ws://%s/ws", os.Getenv("WS_HOST"))
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer conn.Close()

	messageCount := rand.Intn(200)

	for j := 0; j < messageCount; j++ {
		msg, _ := json.Marshal(map[string]string{
			"type":  "message",
			"value": randomString(10, 100),
			"hash":  randomString(33, 33),
		})

		if err := conn.WriteMessage(websocket.TextMessage, msg); err != nil {
			return fmt.Errorf("failed to send message: %w", err)
		}

		_, _, err := conn.ReadMessage()
		if err != nil {
			return fmt.Errorf("failed to read response: %w", err)
		}

		// log.Printf("Client %d received\n", clientID)

		time.Sleep(time.Duration(rand.Intn(100)) * time.Millisecond)
	}

	return nil
}
