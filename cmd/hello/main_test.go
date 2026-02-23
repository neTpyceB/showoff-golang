package main

import (
	"bytes"
	"io"
	"os"
	"testing"
)

func TestMainPrintsHelloMessage(t *testing.T) {
	oldStdout := os.Stdout
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("create pipe: %v", err)
	}

	os.Stdout = w
	main()
	os.Stdout = oldStdout

	if err := w.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	var out bytes.Buffer
	if _, err := io.Copy(&out, r); err != nil {
		t.Fatalf("read output: %v", err)
	}

	want := "Hello from Go (running in Docker)!\n"
	if out.String() != want {
		t.Fatalf("unexpected output: got %q want %q", out.String(), want)
	}
}
