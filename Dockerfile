FROM golang:latest

ENV GO111MODULE=on

WORKDIR /home/app

COPY go.mod /home/app
COPY go.sum /home/app

RUN go mod download

COPY . .

RUN go build -ldflags "-s" -o /home/app/bin/ws cmd/ws/main.go

EXPOSE 8080

ENTRYPOINT ["/home/app/bin/ws"]
