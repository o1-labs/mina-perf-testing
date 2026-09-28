package main

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	logging "github.com/ipfs/go-log/v2"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	lib "itn_orchestrator"
	service "itn_orchestrator/service"
)

func cancelTestStore(t *testing.T) *service.Store {
	t.Helper()
	dsn := os.Getenv("TEST_PG_DSN")
	if dsn == "" {
		t.Skip("TEST_PG_DSN not set")
	}
	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	db.Exec(`DROP TABLE IF EXISTS experiment_state`)
	store, err := service.NewStore(db)
	if err != nil {
		t.Fatalf("NewStore: %v", err)
	}
	return store
}

// TestCancelDuringWaitIsCancelled is the regression test for a cancel that
// lands while the experiment is inside a WaitAction.
//
// RunActions returns *nil* when it sees ctx.Done between steps, and loadRun
// used to inspect the context only on the error path -- so a nil return went
// to FinishWithSuccess and fired the success webhook. The operator cancelled
// and the receiver was told the run succeeded. The last round now ends with a
// wait of up to RoundDurationMin, which is the widest this window gets.
func TestCancelDuringWaitIsCancelled(t *testing.T) {
	for _, tc := range []struct {
		name string
		plan string
	}{
		{
			name: "cancel during a trailing wait",
			plan: `{"action":"wait","params":{"sec":2}}`,
		},
		{
			name: "cancel during a wait with a step after it",
			plan: `{"action":"wait","params":{"sec":2}}` + "\n" + `{"action":"wait","params":{"sec":1}}`,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var bodies []string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				b, _ := io.ReadAll(r.Body)
				mu.Lock()
				bodies = append(bodies, string(b))
				mu.Unlock()
				w.WriteHeader(http.StatusOK)
			}))
			defer srv.Close()

			store := cancelTestStore(t)
			notifier := NewWebhookNotifier(logging.Logger("cancel-wait-test"))
			// httptest listens on loopback, which both the URL check and the
			// dial-time check refuse by design. Both seams are opened, or the
			// "no webhook was delivered" assertion below would pass because
			// the dial was blocked rather than because nothing was sent.
			notifier.validateURL = func(string) error { return nil }
			notifier.checkDialIP = func(net.IP) error { return nil }
			app := &App{Store: store, WebhookNotifier: notifier}

			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			now := time.Now()
			exp := &service.ExperimentState{
				Name: "cancel-wait-" + strings.ReplaceAll(tc.name, " ", "-"),
				// A webhook URL must be set, or the success notification is
				// skipped and the test could pass for the wrong reason.
				WebhookURL: srv.URL,
				Status:     service.Running,
				CreatedAt:  now,
				UpdatedAt:  now,
			}
			if err := store.Add(exp, cancel); err != nil {
				t.Fatalf("Add: %v", err)
			}

			cfg := lib.SetupConfig(ctx, lib.OrchestratorConfig{}, logging.Logger("cancel-wait-run"))
			// The client gate runs before the plan does, and there is no
			// deployment table here, so without this it refuses and the
			// experiment is already finished before the cancel lands.
			cfg.AllowUnverifiedMinaExec = true

			done := make(chan struct{})
			go func() {
				defer close(done)
				app.loadRun(json.NewDecoder(strings.NewReader(tc.plan)), cfg, logging.Logger("cancel-wait-run"))
			}()

			time.Sleep(300 * time.Millisecond)
			if err := store.Cancel(); err != nil {
				t.Fatalf("Cancel: %v", err)
			}

			select {
			case <-done:
			case <-time.After(30 * time.Second):
				t.Fatal("loadRun did not return")
			}

			// Give any webhook goroutine a moment to land.
			time.Sleep(300 * time.Millisecond)

			got := store.AtomicGet()
			if got == nil {
				t.Fatal("no experiment in the store")
			}
			if got.Status != service.Cancelled {
				t.Errorf("status = %q, want %q -- the operator cancelled, so this is not a success",
					got.Status, service.Cancelled)
			}
			if len(got.Errors) != 0 {
				t.Errorf("errors = %v, want none -- a cancellation is not a failure", got.Errors)
			}

			mu.Lock()
			defer mu.Unlock()
			for _, b := range bodies {
				if strings.Contains(b, `"success":true`) {
					t.Errorf("a success webhook was delivered for a cancelled experiment: %s", b)
				}
			}
			if len(bodies) != 0 {
				t.Errorf("want no webhook for a cancelled experiment, got %d: %v", len(bodies), bodies)
			}
		})
	}
}

// A cancel that lands mid-action makes the action return "context canceled",
// which RunExperiment logs as an error. That line belongs in Logs, not in
// Errors: an operator cancel is not a failure.
func TestCancelMidActionIsNotAnError(t *testing.T) {
	online := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer online.Close()

	var mu sync.Mutex
	var bodies []string
	hook := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		bodies = append(bodies, string(b))
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer hook.Close()

	store := cancelTestStore(t)
	notifier := NewWebhookNotifier(logging.Logger("cancel-midaction-test"))
	notifier.validateURL = func(string) error { return nil }
	notifier.checkDialIP = func(net.IP) error { return nil }
	app := &App{Store: store, WebhookNotifier: notifier}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	now := time.Now()
	if err := store.Add(&service.ExperimentState{
		Name: "cancel-midaction", WebhookURL: hook.URL, Status: service.Running,
		CreatedAt: now, UpdatedAt: now,
	}, cancel); err != nil {
		t.Fatalf("Add: %v", err)
	}

	// Log through the store, as CreateExperimentHandler does; with a plain
	// logger the error never reaches AppendErrorF and this passes vacuously.
	log := service.StoreLogging{Store: store, Log: logging.Logger("cancel-midaction-run")}
	cfg := lib.SetupConfig(ctx, lib.OrchestratorConfig{OnlineURL: online.URL}, log)
	cfg.AllowUnverifiedMinaExec = true

	done := make(chan struct{})
	go func() {
		defer close(done)
		app.loadRun(json.NewDecoder(strings.NewReader(`{"action":"discovery","params":{}}`)), cfg, log)
	}()
	time.Sleep(300 * time.Millisecond)
	if err := store.Cancel(); err != nil {
		t.Fatalf("Cancel: %v", err)
	}
	select {
	case <-done:
	case <-time.After(30 * time.Second):
		t.Fatal("loadRun did not return")
	}
	time.Sleep(300 * time.Millisecond)

	got := store.AtomicGet()
	if got.Status != service.Cancelled {
		t.Errorf("status = %q, want %q", got.Status, service.Cancelled)
	}
	if len(got.Errors) != 0 {
		t.Errorf("errors = %q, want none -- a cancellation is not a failure", got.Errors)
	}
	inLogs := false
	for _, l := range got.Logs {
		if strings.Contains(l, "context canceled") {
			inLogs = true
		}
	}
	if !inLogs {
		t.Errorf("the context-canceled message should still be in Logs; logs=%q", got.Logs)
	}
	mu.Lock()
	defer mu.Unlock()
	if len(bodies) != 0 {
		t.Errorf("want no webhook for a cancelled experiment, got %d: %v", len(bodies), bodies)
	}
}
