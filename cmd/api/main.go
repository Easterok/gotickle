package main

import (
	"fmt"
	"math/rand"
	"net/http"
	"time"

	"github.com/labstack/echo/v4"
	"github.com/labstack/echo/v4/middleware"
)

type Request struct {
	Messages string `json:"messages"`
}

func main() {
	e := echo.New()
	e.Use(middleware.Logger())

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

		return c.JSON(http.StatusOK, map[string]string{
			"response": fmt.Sprintf("Response for message: %s", u.Messages),
		})
	})

	e.Logger.Fatal(e.Start(":8081"))
}
