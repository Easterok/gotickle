package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"sync/atomic"
	"time"

	_ "net/http/pprof"

	"easterok.github.com/gotickle/pkg/llm"
	"easterok.github.com/gotickle/pkg/stats"
	"github.com/google/uuid"
	"github.com/gorilla/websocket"
	"github.com/joho/godotenv"
)

const (
	dotenvErr = "error loading .env file"

	writeWait  = 10 * time.Second
	pongWait   = 60 * time.Second
	pingPeriod = (pongWait * 9) / 10
)

var statistic *stats.Stats

var wsUpgrader = websocket.Upgrader{
	ReadBufferSize:  1024,
	WriteBufferSize: 1024,
}

type SocketMessage struct {
	Hash  string `json:"hash"`
	Value string `json:"value"`
	Type  string `json:"type"`
}

type ResponseSocketMessage struct {
	Hash string `json:"hash"`
	Text string `json:"value"`
	Type string `json:"type"`
	Self bool   `json:"self"`
}

type ResponseSocketErrorMessage struct {
	Type      string `json:"type"`
	RequestId string `json:"request_id"`
}

func parseText(b []byte) *SocketMessage {
	var msg SocketMessage

	err := json.Unmarshal(b, &msg)

	if err != nil {
		return nil
	}

	return &msg
}

func responseSelfMessage(msg *SocketMessage) *[]byte {
	self := ResponseSocketMessage{
		Self: true,
		Hash: msg.Hash,
		Type: "text",
	}

	b, err := json.Marshal(self)

	if err != nil {
		return nil
	}

	return &b
}

func typing() []byte {
	self := ResponseSocketMessage{
		Self: false,
		Hash: "-1",
		Text: "Companion is typing",
		Type: "typing",
	}

	b, _ := json.Marshal(self)

	return b
}

func generateErrorResponse() []byte {
	self := ResponseSocketErrorMessage{
		Type: "response_error",
	}

	b, _ := json.Marshal(self)

	return b
}

func generateRegenerateResponse(msg string, id string) []byte {
	self := ResponseSocketMessage{
		Self: false,
		Hash: id,
		Text: msg,
		Type: "regenerate_response",
	}

	b, _ := json.Marshal(self)

	return b
}

func generateResponse(msg string, id string) []byte {
	self := ResponseSocketMessage{
		Self: false,
		Hash: id,
		Text: msg,
		Type: "text",
	}

	b, _ := json.Marshal(self)

	return b
}

type Client struct {
	Incoming chan []byte

	Outgoing chan []byte

	Conn *websocket.Conn

	HttpClient  *http.Client
	HttpContext *llm.Context

	Shutdown   context.Context
	ShutdownFn context.CancelFunc

	ApiCancel context.CancelFunc
}

func NewClient(ws *websocket.Conn) *Client {
	shutdown, shutdownFn := context.WithCancel(context.Background())

	return &Client{
		Incoming: make(chan []byte, 256),
		Outgoing: make(chan []byte, 256),
		Conn:     ws,

		HttpClient: &http.Client{},
		HttpContext: &llm.Context{
			HistoryBuffer: llm.ABuffer[*llm.Message]{
				Size: 30,
				Buff: make([]*llm.Message, 0, 30),
			},
			SystemPrompt: "You are a school teacher",
			UserPrompt:   "Please, get me a new answer for that question",
		},

		Shutdown:   shutdown,
		ShutdownFn: shutdownFn,
	}
}

func (c *Client) Stop() {
	// fmt.Printf("Closing connection...\n")

	c.ShutdownFn()
	c.Conn.Close()
}

func (c *Client) Writer() {
	pingPong := time.NewTicker(pingPeriod)

	defer func() {
		pingPong.Stop()
		c.Stop()
	}()

	for {
		select {
		case <-c.Shutdown.Done():
			return
		case msg, ok := <-c.Outgoing:
			if !ok {
				c.Conn.WriteMessage(websocket.CloseMessage, nil)
				return
			}

			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			err := c.Conn.WriteMessage(websocket.TextMessage, msg)

			if err != nil {
				fmt.Println(err)
				return
			}

			atomic.AddInt64(&statistic.MessagesSent, 1)
		case <-pingPong.C:
			c.Conn.SetWriteDeadline(time.Now().Add(writeWait))
			if err := c.Conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				fmt.Println(err)
				return
			}
		}
	}
}

func (c *Client) Reader() {
	defer func() {
		close(c.Incoming)
		c.Stop()
	}()

outer:
	for {
		_, message, err := c.Conn.ReadMessage()

		if err != nil {
			if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
				fmt.Println(err)
			}
			break
		}

		select {
		case c.Incoming <- message:
			atomic.AddInt64(&statistic.MessagesReceived, 1)
		case <-c.Shutdown.Done():
			break outer
		default:
			break outer
		}
	}
}

func (c *Client) Handler() {
	defer func() {
		if c.ApiCancel != nil {
			c.ApiCancel()
		}

		c.Stop()
	}()

outer:
	for {
		select {
		case <-c.Shutdown.Done():
			break outer
		case in, ok := <-c.Incoming:
			if !ok {
				break outer
			}

			parsed := parseText(in)

			if parsed == nil {
				fmt.Printf("unable to parse message %s\n", string(in))
				continue
			}

			if parsed.Type != "message" && parsed.Type != "retry" && parsed.Type != "regenerate" {
				continue
			}

			regenerateId := ""

			if parsed.Type == "message" {
				self := responseSelfMessage(parsed)

				if self == nil {
					continue
				}

				c.Outgoing <- *self
				c.Outgoing <- typing()

				c.HttpContext.Push(&llm.Message{
					Id:     parsed.Hash,
					Text:   parsed.Value,
					Answer: false,
				})
			} else if parsed.Type == "regenerate" {
				regenerateId = parsed.Hash
			} else if parsed.Type == "retry" {
				c.Outgoing <- typing()
			}

			if c.ApiCancel != nil {
				c.ApiCancel()
			}

			ctx, cancel := context.WithCancel(context.Background())
			c.ApiCancel = cancel

			go c.TriggerApi(ctx, regenerateId)
		}
	}
}

type ApiResponse struct {
	Response string `json:"response"`
}

func (c *Client) TriggerApi(ctx context.Context, regenerateId string) {
	data, err := c.HttpContext.MakeBody(regenerateId)

	if err != nil {
		fmt.Printf("Failed to create body %s", err.Error())
		return
	}

	req, err := http.NewRequestWithContext(ctx, "POST", os.Getenv("API_ENDPOINT"), data)

	if err != nil {
		fmt.Printf("Failed to create request %s", err.Error())
		return
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.HttpClient.Do(req)
	if err != nil || resp.StatusCode != http.StatusOK {
		if ctx.Err() == context.Canceled {
			// fmt.Println("Request was canceled")
		} else {
			atomic.AddInt64(&statistic.ErrorCount, 1)

			// fmt.Printf("ApiError returned status %s, errMsg: %s\n", resp.Status, err)

			if regenerateId == "" {
				select {
				case <-c.Shutdown.Done():
					return
				case c.Outgoing <- generateErrorResponse():
					return
				default:
					return
				}
			}
		}

		return
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		atomic.AddInt64(&statistic.ErrorCount, 1)
		fmt.Println("Error reading response body:", err)
		return
	}

	d := new(ApiResponse)
	err = json.Unmarshal(body, &d)

	if err != nil {
		atomic.AddInt64(&statistic.ErrorCount, 1)
		fmt.Printf("Failed to unmarshal response %s\n", err.Error())
		return
	}

	var r []byte
	id := regenerateId
	txt := d.Response

	if regenerateId == "" {
		id = uuid.NewString()
		r = generateResponse(txt, id)
	} else {
		r = generateRegenerateResponse(txt, regenerateId)
	}

outer:
	for {
		select {
		case <-c.Shutdown.Done():
			break outer
		case c.Outgoing <- r:
			if regenerateId != "" {
				c.HttpContext.UpdateText(id, txt)
			} else {
				c.HttpContext.Push(&llm.Message{
					Answer: true,
					Id:     id,
					Text:   txt,
				})
			}
			break outer
		default:
			break outer
		}
	}
}

func socket(w http.ResponseWriter, r *http.Request) {
	ws, err := wsUpgrader.Upgrade(w, r, nil)

	if err != nil {
		log.Println(err)

		return
	}

	atomic.AddInt64(&statistic.LiveConn, 1)
	atomic.AddInt64(&statistic.TotalConn, 1)

	defer func() {
		atomic.AddInt64(&statistic.LiveConn, -1)
	}()

	client := NewClient(ws)

	client.Conn.SetReadDeadline(time.Now().Add(pongWait))
	client.Conn.SetPongHandler(func(string) error { client.Conn.SetReadDeadline(time.Now().Add(pongWait)); return nil })

	go client.Writer()
	go client.Handler()

	client.Reader()
}

func main() {
	err := godotenv.Load()

	if err != nil {
		log.Fatal(err)
	}

	statistic = stats.NewStatsWithConfig(stats.StatsConfig{
		SnapshotInterval: time.Minute,
		SnapshotCallback: func(s *stats.Stats) {
			fmt.Printf("-----\nTime:%s\nTotalConnections: %d\nLiveConnections: %d\nMessagesSent: %d\nMessagesReceived: %d\nApiErrors: %d\n-----\n", time.Now().UTC().Format("2006-01-02 15:04:05"), s.TotalConn, s.LiveConn, s.MessagesSent, s.MessagesReceived, s.ErrorCount)
		},
	})

	port := "8080"

	defer statistic.Stop()

	go statistic.Start()

	http.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.Error(w, "Not found", http.StatusNotFound)
			return
		}

		if r.Method != http.MethodGet {
			http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)

			return
		}

		http.ServeFile(w, r, "views/ws.html")
	})

	http.HandleFunc("/ws", socket)

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", port), nil))
}
