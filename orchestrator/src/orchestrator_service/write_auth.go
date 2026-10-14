package main

import (
	"fmt"
	"net/http"
	"os"
	"sync"
	"time"

	"github.com/tg123/go-htpasswd"
)

// writeAuth checks HTTP basic auth on the write endpoints against an htpasswd
// file: bcrypt, apr1 (MD5) and SHA lines, the formats the Traefik basic-auth
// middleware in front of the service accepts, so one users secret serves
// both layers. The file is read again when its modification time changes,
// so a rotated secret takes effect without a restart; a file that stops
// being readable keeps the last users that were.
//
// The application enforces this itself, and not only the ingress, because
// an edge control is one annotation away from being lost (ticket 037):
// losing the middleware must cost a redundant layer, not the control plane.
type writeAuth struct {
	path    string
	mu      sync.Mutex
	users   *htpasswd.File
	modTime time.Time
}

func newWriteAuth(path string) (*writeAuth, error) {
	w := &writeAuth{path: path}
	if err := w.reload(); err != nil {
		return nil, err
	}
	return w, nil
}

func (w *writeAuth) reload() error {
	st, err := os.Stat(w.path)
	if err != nil {
		return err
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.users != nil && st.ModTime().Equal(w.modTime) {
		return nil
	}
	users, err := htpasswd.New(w.path, htpasswd.DefaultSystems, func(err error) {
		stdoutLog.Printf("write-auth users file %s: skipping a line: %v", w.path, err)
	})
	if err != nil {
		return fmt.Errorf("reading the write-auth users file %s: %w", w.path, err)
	}
	w.users, w.modTime = users, st.ModTime()
	return nil
}

func (w *writeAuth) match(user, password string) bool {
	if err := w.reload(); err != nil {
		stdoutLog.Printf("write-auth users file not re-read, keeping the last users: %v", err)
	}
	w.mu.Lock()
	users := w.users
	w.mu.Unlock()
	return users != nil && users.Match(user, password)
}

// require wraps a write handler: without valid credentials the request is
// answered with 401 and never reaches the handler. Accepted writes are
// logged with the user name.
func (w *writeAuth) require(h http.Handler) http.Handler {
	return http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		user, password, ok := r.BasicAuth()
		if !ok || !w.match(user, password) {
			rw.Header().Set("WWW-Authenticate", `Basic realm="orchestrator"`)
			writeResponse(rw, http.StatusUnauthorized, APIResponse{
				Errors: []string{"this endpoint needs HTTP basic auth with a user from the orchestrator's write-auth users file"},
				Result: "error",
			})
			return
		}
		stdoutLog.Printf("%s %s by %s", r.Method, r.URL.Path, user)
		h.ServeHTTP(rw, r)
	})
}

// protect applies the write auth when one is configured. The service refuses
// to start without one unless -allow-unauthenticated-writes was given, so a
// nil WriteAuth is an explicit operator choice.
func (a *App) protect(h http.Handler) http.Handler {
	if a.WriteAuth == nil {
		return h
	}
	return a.WriteAuth.require(h)
}
