package main

import (
	"log"
	"net/http"

	"showoff-golang/internal/httpapp"
)

const defaultAddr = ":8080"

var listenAndServe = http.ListenAndServe
var fatalf = log.Fatalf

func run() error {
	return listenAndServe(defaultAddr, httpapp.NewHandler())
}

func main() {
	if err := run(); err != nil {
		fatalf("server error: %v", err)
	}
}
