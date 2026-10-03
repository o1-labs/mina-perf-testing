package itn_orchestrator

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
	"time"
)

func TestParseMina(t *testing.T) {
	if _, err := parseMina("12.1234567890"); err == nil {
		t.Fatal("parsing too long a number")
	}
	if _, err := parseMina("a"); err == nil {
		t.Fatal("not a number")
	}
	if _, err := parseMina(".23"); err == nil {
		t.Fatal("no leading zero parsed")
	}
	if v, err := parseMina("0.23"); err != nil || v != 23e7 {
		t.Fatal("leading zero not parsed")
	}
	if v, err := parseMina("00.002300"); err != nil || v != 23e5 {
		t.Fatal("double leading zero not parsed")
	}
	if v, err := parseMina("123.456789"); err != nil || v != 123456789e3 {
		t.Fatal("double leading zero not parsed")
	}
	if v, err := parseMina("123.456789001"); err != nil || v != 123456789001 {
		t.Fatal("double leading zero not parsed")
	}
	if v, err := parseMina("123"); err != nil || v != 123e9 {
		t.Fatal("no dot not parsed")
	}
}

// A wait step ends when the experiment context is cancelled, not when the
// wait is over.
func TestWaitEndsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	go func() {
		time.Sleep(50 * time.Millisecond)
		cancel()
	}()
	start := time.Now()
	err := WaitAction{}.Run(Config{Ctx: ctx}, json.RawMessage(`{"min": 10}`), nil)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatalf("the wait ended %s after the cancel", time.Since(start))
	}
	if err := (WaitAction{}).Run(Config{Ctx: context.Background()}, json.RawMessage(`{"sec": 0}`), nil); err != nil {
		t.Fatalf("a zero wait returned %v", err)
	}
}
