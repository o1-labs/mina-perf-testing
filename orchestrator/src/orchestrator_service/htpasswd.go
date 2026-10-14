package main

import (
	"bufio"
	"crypto/md5"
	"crypto/sha1"
	"crypto/subtle"
	"encoding/base64"
	"fmt"
	"io"
	"strings"

	"golang.org/x/crypto/bcrypt"
)

// htpasswdUsers holds the user:hash lines of an htpasswd file. The hash
// formats are the ones Apache's htpasswd writes and Traefik's basic-auth
// middleware verifies: bcrypt ($2y$, $2a$, $2b$), apr1 (Apache's MD5
// variant, also `openssl passwd -apr1`) and {SHA} (base64 SHA-1). Plain
// text and crypt(3) lines are refused, so a weak line cannot be added by
// mistake.
type htpasswdUsers map[string]string

func parseHtpasswd(r io.Reader, badLine func(line int, err error)) (htpasswdUsers, error) {
	users := htpasswdUsers{}
	s := bufio.NewScanner(r)
	for n := 1; s.Scan(); n++ {
		line := strings.TrimSpace(s.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		user, hash, ok := strings.Cut(line, ":")
		switch {
		case !ok || user == "":
			badLine(n, fmt.Errorf("not a user:hash line"))
		case !supportedHash(hash):
			badLine(n, fmt.Errorf("user %q: hash is not bcrypt, apr1 or {SHA}", user))
		default:
			users[user] = hash
		}
	}
	return users, s.Err()
}

func supportedHash(hash string) bool {
	return strings.HasPrefix(hash, "$2y$") || strings.HasPrefix(hash, "$2a$") || strings.HasPrefix(hash, "$2b$") ||
		strings.HasPrefix(hash, "$apr1$") || strings.HasPrefix(hash, "{SHA}")
}

// Match reports whether password is the user's. An unknown user costs a
// bcrypt comparison too, so a listing cannot be probed through the timing.
func (u htpasswdUsers) Match(user, password string) bool {
	hash, ok := u[user]
	if !ok {
		_ = bcrypt.CompareHashAndPassword(unknownUserHash, []byte(password))
		return false
	}
	switch {
	case strings.HasPrefix(hash, "$apr1$"):
		return subtle.ConstantTimeCompare([]byte(apr1Hash(password, hash)), []byte(hash)) == 1
	case strings.HasPrefix(hash, "{SHA}"):
		sum := sha1.Sum([]byte(password))
		want := "{SHA}" + base64.StdEncoding.EncodeToString(sum[:])
		return subtle.ConstantTimeCompare([]byte(want), []byte(hash)) == 1
	default:
		return bcrypt.CompareHashAndPassword([]byte(hash), []byte(password)) == nil
	}
}

// unknownUserHash is compared against for users that are not listed.
var unknownUserHash, _ = bcrypt.GenerateFromPassword([]byte("unknown user"), bcrypt.DefaultCost)

// apr1Hash computes Apache's MD5-based password hash ($apr1$salt$hash) for
// password with the salt taken from existing, which is the stored hash.
func apr1Hash(password, existing string) string {
	const magic = "$apr1$"
	salt := strings.TrimPrefix(existing, magic)
	if i := strings.IndexByte(salt, '$'); i >= 0 {
		salt = salt[:i]
	}
	if len(salt) > 8 {
		salt = salt[:8]
	}
	pw := []byte(password)
	sb := []byte(salt)

	alt := md5.Sum(append(append(append([]byte{}, pw...), sb...), pw...))
	h := md5.New()
	h.Write(pw)
	h.Write([]byte(magic))
	h.Write(sb)
	for i := len(pw); i > 0; i -= 16 {
		n := i
		if n > 16 {
			n = 16
		}
		h.Write(alt[:n])
	}
	for i := len(pw); i > 0; i >>= 1 {
		if i&1 != 0 {
			h.Write([]byte{0})
		} else {
			h.Write(pw[:1])
		}
	}
	final := h.Sum(nil)
	for i := 0; i < 1000; i++ {
		h := md5.New()
		if i&1 != 0 {
			h.Write(pw)
		} else {
			h.Write(final)
		}
		if i%3 != 0 {
			h.Write(sb)
		}
		if i%7 != 0 {
			h.Write(pw)
		}
		if i&1 != 0 {
			h.Write(final)
		} else {
			h.Write(pw)
		}
		final = h.Sum(nil)
	}

	const itoa64 = "./0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"
	var out strings.Builder
	to64 := func(v uint32, n int) {
		for ; n > 0; n-- {
			out.WriteByte(itoa64[v&0x3f])
			v >>= 6
		}
	}
	for _, t := range [][3]int{{0, 6, 12}, {1, 7, 13}, {2, 8, 14}, {3, 9, 15}, {4, 10, 5}} {
		to64(uint32(final[t[0]])<<16|uint32(final[t[1]])<<8|uint32(final[t[2]]), 4)
	}
	to64(uint32(final[11]), 2)
	return magic + salt + "$" + out.String()
}
