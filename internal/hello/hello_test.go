package hello

import "testing"

func TestMessage(t *testing.T) {
	want := "Hello from Go (running in Docker)!"

	if got := Message(); got != want {
		t.Fatalf("Message() = %q, want %q", got, want)
	}
}
