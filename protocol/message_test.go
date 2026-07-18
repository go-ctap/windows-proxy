package protocol

import (
	"bytes"
	"math"
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

func TestNewMessageRejectsOversizedPayload(t *testing.T) {
	if _, err := NewMessage(CommandStart, make([]byte, math.MaxUint16)); err == nil {
		t.Fatal("expected oversized payload error")
	}
}

func TestWriteToRejectsOversizedMutatedPayload(t *testing.T) {
	message, err := NewMessage(CommandStart, nil)
	if err != nil {
		t.Fatal(err)
	}
	message.Data = make([]byte, math.MaxUint16+1)

	var wire bytes.Buffer
	if _, err := message.WriteTo(&wire); err == nil {
		t.Fatal("expected oversized payload error")
	}
	if wire.Len() != 0 {
		t.Fatalf("wrote %d bytes before rejecting payload", wire.Len())
	}
}
