package httpapp

import (
	"net/http"

	"showoff-golang/internal/hello"
)

func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /", home)
	return mux
}

func home(w http.ResponseWriter, _ *http.Request) {
	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	_, _ = w.Write([]byte(hello.Message() + "\n"))
}
