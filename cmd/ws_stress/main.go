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
	clientCount = 2_000
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
	time.Sleep(time.Millisecond * 50)

	u := fmt.Sprintf("ws://%s/ws", os.Getenv("WS_HOST"))
	conn, _, err := websocket.DefaultDialer.Dial(u, nil)
	if err != nil {
		return fmt.Errorf("failed to connect: %w", err)
	}
	defer conn.Close()

	fmt.Printf("Running client: %d\n", clientID)

	t := time.NewTicker(time.Second * 2)
	d := time.NewTicker(time.Minute)

	for {
		select {
		case <-d.C:
			return fmt.Errorf("done")
		case <-t.C:
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
		}
	}
}
