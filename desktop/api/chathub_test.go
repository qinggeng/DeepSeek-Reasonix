package api

import (
	"testing"
	"time"

	"reasonix/internal/event"
)

func TestStreamHub_RegisterLookupUnregister(t *testing.T) {
	hub := NewStreamHub(10)

	// Register
	ch := hub.Register("stream-1")
	if ch == nil {
		t.Fatal("expected non-nil channel")
	}

	// Lookup
	got := hub.Lookup("stream-1")
	if got == nil {
		t.Fatal("expected to find stream-1")
	}

	// Len
	if hub.Len() != 1 {
		t.Errorf("expected 1 stream, got %d", hub.Len())
	}

	// Unregister
	hub.Unregister("stream-1")

	// Verify channel is closed
	select {
	case _, ok := <-ch:
		if ok {
			t.Error("expected closed channel after unregister")
		}
	default:
		// channel was already blocking/closed; try receiving
		_, ok := <-ch
		if ok {
			t.Error("expected closed channel")
		}
	}

	if hub.Len() != 0 {
		t.Errorf("expected 0 streams after unregister, got %d", hub.Len())
	}
}

func TestStreamHub_LookupNotFound(t *testing.T) {
	hub := NewStreamHub(10)
	got := hub.Lookup("nonexistent")
	if got != nil {
		t.Error("expected nil for nonexistent stream")
	}
}

func TestStreamHub_UnregisterTwice(t *testing.T) {
	hub := NewStreamHub(10)
	hub.Register("s1")
	hub.Unregister("s1")
	hub.Unregister("s1") // should not panic
}

func TestStreamHub_EventsCanBeRead(t *testing.T) {
	hub := NewStreamHub(10)

	ch := hub.Register("s1")
	ch <- event.Event{Kind: event.TurnStarted}
	ch <- event.Event{Kind: event.Text, Text: "hello"}

	// Read back
	e1 := <-hub.Lookup("s1")
	if e1.Kind != event.TurnStarted {
		t.Errorf("expected TurnStarted, got %v", e1.Kind)
	}
	e2 := <-hub.Lookup("s1")
	if e2.Kind != event.Text || e2.Text != "hello" {
		t.Errorf("expected Text/hello, got %v/%s", e2.Kind, e2.Text)
	}

	hub.Unregister("s1")
}

func TestStreamHub_BufferOverflow(t *testing.T) {
	hub := NewStreamHub(2) // small buffer

	ch := hub.Register("s1")
	// Fill buffer
	ch <- event.Event{Kind: event.TurnStarted}
	ch <- event.Event{Kind: event.Text, Text: "a"}

	// Third write should block briefly then succeed or drop
	// We use a timeout to avoid hanging
	select {
	case ch <- event.Event{Kind: event.Text, Text: "overflow"}:
		// succeeded (channel may have been drained by test or buffer is exact)
	case <-time.After(100 * time.Millisecond):
		t.Log("channel blocked as expected on full buffer")
	}
	hub.Unregister("s1")
}
