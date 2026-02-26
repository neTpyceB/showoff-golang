package main

import (
	"context"
	"log"
	"net/http"
	"os"

	"showoff-golang/internal/httpapp"
)

const defaultAddr = ":8080"

var listenAndServe = http.ListenAndServe
var fatalf = log.Fatalf
var newPostgresHandler = httpapp.NewPostgresHandler

func run() error {
	handler := httpapp.NewHandler()

	if databaseURL := os.Getenv("DATABASE_URL"); databaseURL != "" {
		dbHandler, closeFn, err := newPostgresHandler(context.Background(), databaseURL)
		if err != nil {
			return err
		}
		defer func() { _ = closeFn() }()
		handler = dbHandler
	}

	return listenAndServe(defaultAddr, handler)
}

func main() {
	if err := run(); err != nil {
		fatalf("server error: %v", err)
	}
}
