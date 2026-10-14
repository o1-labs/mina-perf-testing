package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"
)

func usersFile(t *testing.T, lines string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "users")
	if err := os.WriteFile(path, []byte(lines), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func bcryptLine(t *testing.T, user, password string) string {
	t.Helper()
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.MinCost)
	if err != nil {
		t.Fatal(err)
	}
	return user + ":" + string(h) + "\n"
}

func post(h http.Handler, user, password string) *httptest.ResponseRecorder {
	r := httptest.NewRequest(http.MethodPost, "/api/v0/experiment/cancel", nil)
	if user != "" {
		r.SetBasicAuth(user, password)
	}
	w := httptest.NewRecorder()
	h.ServeHTTP(w, r)
	return w
}

func TestWriteAuthAdmitsOnlyListedUsers(t *testing.T) {
	// A bcrypt line (htpasswd -B) and an apr1 line (openssl passwd -apr1,
	// password "local"), the two formats operators generate.
	path := usersFile(t, bcryptLine(t, "n8n", "s3cret")+"local:$apr1$Yx1rZ0Jt$heeQusHw02RI4rlW2Wh.F/\n")
	auth, err := newWriteAuth(path)
	if err != nil {
		t.Fatal(err)
	}
	reached := 0
	h := auth.require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { reached++ }))

	for _, c := range []struct {
		user, password string
		want           int
	}{
		{"", "", http.StatusUnauthorized},
		{"n8n", "wrong", http.StatusUnauthorized},
		{"nobody", "s3cret", http.StatusUnauthorized},
		{"n8n", "s3cret", http.StatusOK},
		{"local", "local", http.StatusOK},
	} {
		w := post(h, c.user, c.password)
		if w.Code != c.want {
			t.Errorf("%q/%q: %d, want %d (%s)", c.user, c.password, w.Code, c.want, w.Body.String())
		}
		if c.want == http.StatusUnauthorized && w.Header().Get("WWW-Authenticate") == "" {
			t.Errorf("%q/%q: 401 without WWW-Authenticate", c.user, c.password)
		}
	}
	if reached != 2 {
		t.Errorf("the handler ran %d times, want 2", reached)
	}
}

// A rotated users file takes effect without a restart.
func TestWriteAuthFollowsTheUsersFile(t *testing.T) {
	path := usersFile(t, bcryptLine(t, "old", "pw"))
	auth, err := newWriteAuth(path)
	if err != nil {
		t.Fatal(err)
	}
	h := auth.require(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {}))
	if w := post(h, "old", "pw"); w.Code != http.StatusOK {
		t.Fatalf("before rotation: %d", w.Code)
	}
	if err := os.WriteFile(path, []byte(bcryptLine(t, "new", "pw")), 0o600); err != nil {
		t.Fatal(err)
	}
	// Make the change visible even on a coarse-grained file system clock.
	later := time.Now().Add(2 * time.Second)
	if err := os.Chtimes(path, later, later); err != nil {
		t.Fatal(err)
	}
	if w := post(h, "old", "pw"); w.Code != http.StatusUnauthorized {
		t.Errorf("the removed user still passes: %d", w.Code)
	}
	if w := post(h, "new", "pw"); w.Code != http.StatusOK {
		t.Errorf("the added user is refused: %d", w.Code)
	}
}

// Without a users file the write endpoints are refused at startup, not
// served open by accident; a nil WriteAuth is the explicit opt-out.
func TestWriteAuthMissingFileIsAnError(t *testing.T) {
	if _, err := newWriteAuth(filepath.Join(t.TempDir(), "absent")); err == nil {
		t.Fatal("an absent users file was accepted")
	}
	a := &App{}
	if w := post(a.protect(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {})), "", ""); w.Code != http.StatusOK {
		t.Errorf("with no WriteAuth the request did not pass: %d", w.Code)
	}
}
