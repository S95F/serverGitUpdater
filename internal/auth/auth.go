package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"
	"golang.org/x/term"
)

const (
	SessionCookie = "sgu_session"
	CSRFCookie    = "sgu_csrf"
	CSRFHeader    = "X-CSRF-Token"
)

func HashPassword(pw string) (string, error) {
	if len(pw) < 8 {
		return "", errors.New("password must be at least 8 characters")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(pw), 12)
	if err != nil {
		return "", err
	}
	return string(h), nil
}

func VerifyPassword(hash, pw string) bool {
	return bcrypt.CompareHashAndPassword([]byte(hash), []byte(pw)) == nil
}

func ReadPasswordStdin(prompt string) (string, error) {
	fmt.Print(prompt)
	b, err := term.ReadPassword(0)
	fmt.Println()
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// Token format: base64(user) "." base64(expiryUnix) "." base64(hmac)
// Signed with SessionSecret. Stateless cookie.
func MakeSessionToken(secret, user string, ttl time.Duration) (string, error) {
	if secret == "" {
		return "", errors.New("empty session secret")
	}
	exp := time.Now().Add(ttl).Unix()
	u := base64.RawURLEncoding.EncodeToString([]byte(user))
	e := base64.RawURLEncoding.EncodeToString([]byte(strconv.FormatInt(exp, 10)))
	mac := signHMAC(secret, u+"."+e)
	return u + "." + e + "." + mac, nil
}

func ParseSessionToken(secret, token string) (string, error) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", errors.New("malformed token")
	}
	expected := signHMAC(secret, parts[0]+"."+parts[1])
	if !hmac.Equal([]byte(expected), []byte(parts[2])) {
		return "", errors.New("bad signature")
	}
	expBytes, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", err
	}
	exp, err := strconv.ParseInt(string(expBytes), 10, 64)
	if err != nil {
		return "", err
	}
	if time.Now().Unix() > exp {
		return "", errors.New("expired")
	}
	uBytes, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return "", err
	}
	return string(uBytes), nil
}

func signHMAC(secret, payload string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(payload))
	return base64.RawURLEncoding.EncodeToString(m.Sum(nil))
}

func NewCSRFToken() string {
	b := make([]byte, 24)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

// LoginLimiter throttles login attempts per remote IP.
type LoginLimiter struct {
	mu       sync.Mutex
	attempts map[string]*attemptState
	max      int
	window   time.Duration
}

type attemptState struct {
	count    int
	resetAt  time.Time
	lockedAt time.Time
}

func NewLoginLimiter(max int, window time.Duration) *LoginLimiter {
	return &LoginLimiter{
		attempts: map[string]*attemptState{},
		max:      max,
		window:   window,
	}
}

func (l *LoginLimiter) Allow(key string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	st, ok := l.attempts[key]
	if !ok || now.After(st.resetAt) {
		l.attempts[key] = &attemptState{count: 0, resetAt: now.Add(l.window)}
		return true
	}
	return st.count < l.max
}

func (l *LoginLimiter) Record(key string, success bool) {
	l.mu.Lock()
	defer l.mu.Unlock()
	now := time.Now()
	st, ok := l.attempts[key]
	if !ok {
		st = &attemptState{resetAt: now.Add(l.window)}
		l.attempts[key] = st
	}
	if success {
		delete(l.attempts, key)
		return
	}
	st.count++
	if st.count >= l.max {
		st.lockedAt = now
	}
}

func ClientIP(r *http.Request) string {
	if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
		if i := strings.Index(xff, ","); i > 0 {
			return strings.TrimSpace(xff[:i])
		}
		return strings.TrimSpace(xff)
	}
	if ip := r.Header.Get("X-Real-IP"); ip != "" {
		return ip
	}
	host := r.RemoteAddr
	if i := strings.LastIndex(host, ":"); i > 0 {
		host = host[:i]
	}
	return host
}
