package llm

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
)

type Message struct {
	Text   string
	Id     string
	Answer bool
}

type ABuffer[T any] struct {
	Size int
	Buff []T
}

func (a *ABuffer[T]) Push(el T) {
	if len(a.Buff) >= a.Size {
		a.Buff = a.Buff[1:]
	}

	a.Buff = append(a.Buff, el)
}

func NewABuff[T any](size int) ABuffer[T] {
	return ABuffer[T]{
		Size: size,
		Buff: make([]T, 0, size),
	}
}

func (a *ABuffer[T]) GetAll() []T {
	return a.Buff
}

type Context struct {
	HistoryBuffer ABuffer[*Message]

	UserPrompt   string
	SystemPrompt string
}

type ContextConfig struct {
	BufferSize int
}

func (c *Context) MakeBody(from string) (*bytes.Buffer, error) {
	history := []string{}

	all := c.HistoryBuffer.GetAll()

	for i := 0; i < len(all); i++ {
		msg := all[i]

		user := "user"

		if msg.Answer {
			user = "assistant"
		}

		history = append(history, fmt.Sprintf("%s: %s", user, msg.Text))

		if msg.Id == from {
			break
		}
	}

	// a := strings.Join(history, "\n")

	// fmt.Println("----")
	// fmt.Println(a)
	// fmt.Println("----")

	data, err := json.Marshal(map[string]string{
		"user_prompt":   c.UserPrompt,
		"system_prompt": c.SystemPrompt,
		"history":       strings.Join(history, "\n"),
	})

	if err != nil {
		return nil, err
	}

	return bytes.NewBuffer(data), nil
}

func (c *Context) Push(t *Message) {
	c.HistoryBuffer.Push(t)
}

func (c *Context) UpdateText(id string, text string) {
	all := c.HistoryBuffer.GetAll()

	for _, msg := range all {
		if msg.Answer && msg.Id == id {
			msg.Text = text
		}
	}
}
