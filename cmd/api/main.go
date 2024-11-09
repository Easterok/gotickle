package main

import (
	"fmt"
	"math/rand"
	"net/http"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/labstack/echo/v4"
)

type Request struct {
	UserPrompt   string `json:"user_prompt"`
	SystemPrompt string `json:"system_prompt"`
	History      string `json:"history"`
}

func main() {
	e := echo.New()
	// e.Use(middleware.Logger())

	e.POST("/", func(c echo.Context) error {
		u := new(Request)

		if err := c.Bind(u); err != nil {
			fmt.Println(err.Error())

			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": "Bad Request",
			})
		}

		randomDuration := 0.1 + rand.Float64()*(2.0-0.1)
		duration := time.Duration(randomDuration * float64(time.Second))

		time.Sleep(duration)

		badly := rand.Intn(2)

		if badly == 1 {
			return c.JSON(http.StatusBadRequest, map[string]string{
				"error": "Bad Request",
			})
		}

		id := uuid.NewString()
		hash := strings.Split(id, "-")[0]

		return c.JSON(http.StatusOK, map[string]string{
			"response": fmt.Sprintf("blablabla %s", hash),
		})
	})

	e.Logger.Fatal(e.Start(":8081"))
}
