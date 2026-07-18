package protocol

import (
	"bytes"
	"testing"

	"github.com/fxamacker/cbor/v2"
)

func TestMessageRoundTrip(t *testing.T) {
	wantPath := `\\?\hid#device`
	message, err := NewMessage(CommandStart, wantPath)
	if err != nil {
		t.Fatal(err)
	}

	var wire bytes.Buffer
	if _, err := message.WriteTo(&wire); err != nil {
		t.Fatal(err)
	}

	got, err := ParseMessage(&wire)
	if err != nil {
		t.Fatal(err)
	}
	if got.Command != CommandStart {
		t.Fatalf("command = %d, want %d", got.Command, CommandStart)
	}

	var gotPath string
	if err := cbor.Unmarshal(got.Data, &gotPath); err != nil {
		t.Fatal(err)
	}
	if gotPath != wantPath {
		t.Fatalf("path = %q, want %q", gotPath, wantPath)
	}
}
