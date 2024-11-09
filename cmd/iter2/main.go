package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"sync/atomic"

	_ "net/http/pprof"

	"github.com/coder/websocket"
)

var count int64

func wsSocket(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}

	atomic.AddInt64(&count, 1)

	defer func() {
		atomic.AddInt64(&count, -1)
		c.CloseNow()
	}()

	if count%2 == 0 {
		fmt.Printf("Connection count %d", count)
	}

	for {
		err := echo(c)

		errs := websocket.CloseStatus(err)
		if errs == websocket.StatusNormalClosure || errs == websocket.StatusGoingAway {
			return
		}

		if err != nil {
			log.Printf("failed to echo with %v\n", err)
			return
		}
	}
}

func echo(c *websocket.Conn) error {
	ctx := context.Background()

	_, r, err := c.Reader(ctx)
	if err != nil {
		return err
	}

	msg, err := io.ReadAll(r)

	if err != nil {
		return err
	}

	var v interface{}

	err = json.Unmarshal(msg, &v)

	fmt.Println(v)

	return err
}

func main() {
	port := "8080"

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

	http.HandleFunc("/ws", wsSocket)

	log.Fatal(http.ListenAndServe(fmt.Sprintf(":%s", port), nil))
}
