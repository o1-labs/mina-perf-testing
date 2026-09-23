package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gorilla/mux"
	"gorm.io/driver/postgres"
	"gorm.io/gorm"

	logging "github.com/ipfs/go-log/v2"

	service "itn_orchestrator/service"

	lib "itn_orchestrator"
)

// App holds application-wide dependencies
type App struct {
	Router          *mux.Router
	Store           *service.Store
	Config          *lib.OrchestratorConfig
	WebhookNotifier *WebhookNotifier
}

func (a *App) initializeRoutes() {
	log.Println("Registering routes...")

	// Initialize handlers
	createHandler := &CreateExperimentHandler{
		Store:  a.Store,
		Config: a.Config,
		App:    a,
	}
	infoHandler := &InfoExperimentHandler{
		Store: a.Store,
	}
	statusHandler := &StatusHandler{
		Store: a.Store,
	}
	cancelHandler := &CancelHandler{
		Store: a.Store,
	}

	// Register routes with new handlers
	a.Router.Handle("/api/v0/experiment/run", createHandler).Methods(http.MethodPost)
	a.Router.Handle("/api/v0/experiment/test", infoHandler).Methods(http.MethodPost)
	a.Router.Handle("/api/v0/experiment/status", statusHandler).Methods(http.MethodGet)
	a.Router.Handle("/api/v0/experiment/cancel", cancelHandler).Methods(http.MethodPost)
}

// allowPrivateWebhooks mirrors the -allow-private-webhooks flag. It is a
// package-level switch rather than a config-file field so that the default
// (refuse private destinations) applies even to a config written before the
// option existed.
var allowPrivateWebhooks bool

// Initialize opens the DB and sets up routes.
func (a *App) Initialize(connStr string, config lib.OrchestratorConfig) {
	var err error
	db, err := gorm.Open(postgres.Open(connStr), &gorm.Config{})
	if err != nil {
		log.Fatalf("Cannot open DB: %v", err)
	}
	sqlDB, err := db.DB()
	if err != nil {
		log.Fatalf("Cannot get generic database object: %v", err)
	}
	if err = sqlDB.Ping(); err != nil {
		log.Fatalf("Cannot connect to DB: %v", err)
	}
	a.Router = mux.NewRouter()
	store, err := service.NewStore(db)
	if err != nil {
		log.Fatalf("Cannot initialise experiment store: %v", err)
	}
	a.Store = store
	a.Config = &config
	a.WebhookNotifier = NewWebhookNotifier(logging.Logger("webhook"))
	a.WebhookNotifier.allowPrivate = allowPrivateWebhooks
	a.initializeRoutes()
}

// Run serves until the process is asked to stop, then shuts the server down
// and returns.
//
// It returns rather than calling log.Fatalf, because os.Exit skips every
// deferred call: with the old shape Store.Close was dead code, so the
// orchestrator lock and the database pool were only ever released by the
// connection dying.
func (a *App) Run(address string) {
	log.Println("Starting orchestrator service...")

	srv := &http.Server{Addr: address, Handler: a.Router}

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, syscall.SIGINT, syscall.SIGTERM)

	serveErr := make(chan error, 1)
	go func() {
		log.Printf("Starting server on %s", address)
		if err := srv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serveErr <- err
			return
		}
		serveErr <- nil
	}()

	select {
	case err := <-serveErr:
		if err != nil {
			log.Printf("Server failed: %v", err)
		}
	case sig := <-stop:
		log.Printf("Received %s, shutting down", sig)
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := srv.Shutdown(ctx); err != nil {
			log.Printf("Shutdown: %v", err)
		}
	}
}

// isCancellation reports whether err is the operator stopping the experiment
// rather than the experiment failing. RunExperiment returns the context error
// unchanged in some paths and wrapped in an *OrchestratorError in others, so
// errors.Is is used rather than an equality check; OrchestratorError.Unwrap
// makes that reach the cause.
//
// DeadlineExceeded is deliberately not treated as a cancel. The experiment
// context carries no deadline today, but if one is ever added, a run that
// times out is a failure and must be reported as one.
func isCancellation(err error) bool {
	return errors.Is(err, context.Canceled)
}

// clientEvidence is everything known about the pairing of the mina client and
// the deployed daemon, gathered by verifyMinaClient and judged by
// decideMinaClient.
type clientEvidence struct {
	// extractedPath is the client taken from the deployed daemon image, and
	// extractErr is why that failed. Exactly one is set.
	extractedPath string
	extractErr    error
	// extractedCommit is the commit that client reports, with extractedErr
	// set when it reports none -- a binary that cannot execute.
	extractedCommit    string
	extractedCommitErr error
	// release is the deployment release recorded in the database.
	release    string
	releaseErr error
	// configuredExec is the client from the configuration, the fallback when
	// extraction fails, and configuredCommit is what it reports.
	configuredExec      string
	configuredCommit    string
	configuredCommitErr error
	// allowUnverified is the operator accepting an unproven pairing.
	allowUnverified bool
}

// decideMinaClient judges the evidence.
//
// A client built from a different Mina commit than the daemon does not error:
// the daemon cannot bin_prot-decode the query, fund-keys retries every two
// minutes, and the experiment sits at step 0 reporting nothing. That is how
// the 2026-08-25 run was lost.
//
// What decides is the commit, never the tag. Refusing on a failed extraction
// alone had it backwards -- it refused the safe case (extraction failed, but
// the configured client is the very build the deployment names) and allowed
// the dangerous one (extraction succeeded onto a binary that will not run).
//
// It returns the client to use and a warning to file, or the refusal.
func decideMinaClient(e clientEvidence) (execPath, warning string, err error) {
	if e.extractErr == nil {
		// The client came out of the daemon's own image, so the pairing is
		// right by construction -- provided the binary actually runs.
		if e.extractedCommitErr != nil {
			return "", "", fmt.Errorf("the client taken from the deployed daemon image does not report a version (%v);"+
				" it cannot be executed, so the experiment would hang in fund-keys rather than fail",
				e.extractedCommitErr)
		}
		return e.extractedPath, "", nil
	}

	msg := fmt.Sprintf("Could not take the mina client from the deployed daemon image: %v.", e.extractErr)

	switch {
	case e.configuredCommitErr != nil:
		msg += fmt.Sprintf(" The configured client %q does not report a version either: %v.",
			e.configuredExec, e.configuredCommitErr)
	case e.releaseErr != nil:
		msg += fmt.Sprintf(" The deployment release cannot be read (%v), so the configured client %q (commit %s)"+
			" cannot be checked against it.", e.releaseErr, e.configuredExec, e.configuredCommit)
	default:
		want := releaseCommit(e.release)
		switch {
		case want == "":
			msg += fmt.Sprintf(" The deployment release %q carries no commit, so the configured client %q (commit %s)"+
				" cannot be checked against it.", e.release, e.configuredExec, e.configuredCommit)
		case commitsMatch(want, e.configuredCommit):
			// The safe case: the configured client is the build the
			// deployment names, so the extraction failure costs nothing.
			return e.configuredExec, msg + fmt.Sprintf(" Continuing: the configured client %q is commit %s,"+
				" which is the commit the deployed release %q names.",
				e.configuredExec, e.configuredCommit, e.release), nil
		default:
			msg += fmt.Sprintf(" The configured client %q is commit %s, but the deployed release %q names %s.",
				e.configuredExec, e.configuredCommit, e.release, want)
		}
	}

	if e.allowUnverified {
		return e.configuredExec, msg + fmt.Sprintf(" Continuing with the configured client %q because"+
			" allowUnverifiedMinaExec is set; if it does not match the daemon, funding will hang rather"+
			" than fail.", e.configuredExec), nil
	}

	return "", "", fmt.Errorf("%s Refusing to run: a mismatched client and daemon hang in fund-keys rather than failing."+
		" Fix the deployment release recorded in the database, or set allowUnverifiedMinaExec to accept the risk.", msg)
}

// verifyMinaClient gathers the evidence, applies the decision, and records it.
func (a *App) verifyMinaClient(config *lib.Config, log logging.StandardLogger) error {
	e := clientEvidence{
		configuredExec:  config.MinaExec,
		allowUnverified: config.AllowUnverifiedMinaExec,
	}
	e.release, e.releaseErr = getLatestDeploymentRelease(a.Store.DB)
	e.extractedPath, e.extractErr = getMinaExecutablePath(config.Ctx, a.Store.DB, log)
	if e.extractErr == nil {
		e.extractedCommit, e.extractedCommitErr = minaCommit(e.extractedPath)
	} else {
		e.configuredCommit, e.configuredCommitErr = minaCommit(config.MinaExec)
	}

	execPath, warning, err := decideMinaClient(e)
	if err != nil {
		return err
	}

	config.MinaExec = execPath
	if warning != "" {
		// "%s", not the message as the format: both of these are printf-style,
		// and a wrapped *url.Error carries the manifest URL, whose percent
		// escapes ("3.3.0%2Balpha1-...") were otherwise read as verbs and
		// stored as "3.3.0%!B(MISSING)alpha1". It is also a go vet failure
		// from Go 1.24 on.
		log.Warnf("%s", warning)
		a.Store.AppendWarningF("%s", warning)
	}

	commit := e.extractedCommit
	source := "taken from the deployed daemon image"
	if e.extractErr != nil {
		commit, source = e.configuredCommit, "from the configuration"
	}
	a.Store.AppendLogF("Using mina client %s (commit %s), %s", config.MinaExec, commit, source)
	return nil
}

// stdoutLog is the stdlib logger under a name the `log logging.StandardLogger`
// parameters do not shadow.
var stdoutLog = log.Default()

func (a *App) loadRun(inDecoder *json.Decoder, config lib.Config, log logging.StandardLogger) {
	if err := a.verifyMinaClient(&config, log); err != nil {
		// An operator cancel during the extraction is a cancel, not a failed
		// pairing: the download can take minutes, and POST /cancel during it
		// used to be filed as "error" with the refusal text.
		if isCancellation(err) || config.Ctx.Err() != nil {
			a.Store.FinishWithCancel()
			return
		}

		orchErr := &lib.OrchestratorError{Message: err.Error(), Code: 9}
		// The stdlib logger, not log.Errorf: that logger appends to the
		// experiment's errors too, and FinishWithError is about to record this
		// message, so logging it there files the same refusal twice. Printing
		// it is still necessary -- otherwise the only record is the DB write,
		// and an operator tailing the pod log sees the extraction line and
		// then silence.
		stdoutLog.Printf("%s", orchErr.Message)
		if experiment := a.Store.FinishWithError(orchErr); experiment != nil && experiment.WebhookURL != "" {
			go a.WebhookNotifier.SendErrorNotification(
				context.Background(),
				experiment.WebhookURL,
				experiment.Name,
				orchErr.Message,
				experiment.Warnings,
			)
		}
		return
	}

	err := lib.RunExperiment(inDecoder, config, log)

	// A cancel is not always an error. RunActions returns *nil* when it sees
	// ctx.Done between steps (orchestrator.go), so a cancel landing during a
	// WaitAction -- a plain time.Sleep, and the last round now ends with one
	// lasting up to RoundDurationMin -- reached FinishWithSuccess and fired the
	// success webhook. The operator cancelled and the receiver was told the
	// experiment succeeded.
	if err == nil && config.Ctx.Err() != nil {
		a.Store.FinishWithCancel()
		return
	}

	if err != nil {
		// POST /cancel cancels the experiment context, which surfaces here as
		// an ordinary error. The operator asked for it, so it is a cancellation
		// and not a failure: no Errors entry, no error webhook, terminal status
		// "cancelled".
		// The context is checked as well as the error: a step that returns
		// its own error after the cancel has landed loses the cause, and the
		// operator still asked for the stop.
		if isCancellation(err) || config.Ctx.Err() != nil {
			a.Store.FinishWithCancel()
			return
		}
		var orchErr *lib.OrchestratorError
		var ok bool
		if orchErr, ok = err.(*lib.OrchestratorError); !ok {
			errMsg := fmt.Sprintf("Experiment failed: %v", err)
			orchErr = &lib.OrchestratorError{
				Message: errMsg,
				Code:    9,
			}
		}
		if experiment := a.Store.FinishWithError(orchErr); experiment.WebhookURL != "" {
			// Send error webhook notification
			go a.WebhookNotifier.SendErrorNotification(
				context.Background(),
				experiment.WebhookURL,
				experiment.Name,
				orchErr.Message,
				experiment.Warnings,
			)
		}
		return
	}
	if experiment := a.Store.FinishWithSuccess(); experiment.WebhookURL != "" {
		// Send success webhook notification
		go a.WebhookNotifier.SendSuccessNotification(
			context.Background(),
			experiment.WebhookURL,
			experiment.Name,
			experiment.Warnings,
		)
	}
}

func main() {

	// Define a -conn flag for the Postgres connection string
	connStr := flag.String("conn", "", "Postgres connection string (e.g. \"host=... user=... password=... dbname=... sslmode=disable\")")
	configFilename := flag.String("config", "", "Path to the config file")
	address := flag.String("address", ":8080", "Address to run the server on")
	flag.BoolVar(&allowPrivateWebhooks, "allow-private-webhooks", false,
		"permit webhook destinations on private (RFC1918/RFC4193) addresses; loopback and link-local stay refused")

	flag.Parse()

	if *connStr == "" {
		fmt.Fprintln(os.Stderr, "Usage: go run main.go -conn=\"<connection string>\"")
		os.Exit(1)
	}

	config := lib.LoadAppConfig(*configFilename)

	logging.SetupLogging(logging.Config{
		Format: logging.ColorizedOutput,
		Stderr: true,
		Stdout: false,
		Level:  logging.LogLevel(config.LogLevel),
		File:   config.LogFile,
	})

	app := &App{}
	app.Initialize(*connStr, config)
	sqlDB, err := app.Store.DB.DB()
	if err != nil {
		log.Fatalf("Failed to get generic database object: %v", err)
	}
	// Release the single-orchestrator advisory lock before the pool goes away.
	defer app.Store.Close()
	defer sqlDB.Close()

	app.Run(*address)

}
