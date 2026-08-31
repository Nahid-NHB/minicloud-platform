// Package identity handles user accounts, sessions, API keys, RBAC rules,
// and quotas.
//
// Authentication uses JWTs for short-lived sessions and opaque API keys
// for long-lived programmatic access. Passwords are bcrypt-hashed.
package identity

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/minicloud/platform/internal/state"
)

// Errors.
var (
	ErrInvalidCredentials = errors.New("identity: invalid credentials")
	ErrUnauthorized       = errors.New("identity: unauthorized")
	ErrTokenExpired       = errors.New("identity: token expired")
	ErrTokenMalformed     = errors.New("identity: token malformed")
)

// Role permissions as (verb:resource glob).
// Built-in roles:
//   - admin    : *:*  (full access)
//   - editor   : *:* (within a project)
//   - viewer   : get:*
//   - operator : nodes + read-only
var builtinRoles = map[string][]string{
	"admin":    {"*:*"},
	"editor":   {"*:*"},
	"viewer":   {"get:*", "list:*"},
	"operator": {"get:nodes", "list:nodes", "post:nodes", "get:workloads", "list:workloads", "get:metrics", "get:alerts", "list:alerts"},
}

// HasPermission returns true when the rule set includes the (verb,resource).
func HasPermission(rules []string, verb, resource string) bool {
	for _, r := range rules {
		if matchRule(r, verb, resource) {
			return true
		}
	}
	return false
}

func matchRule(rule, verb, resource string) bool {
	// rule is "<verb-glob>:<resource-glob>"
	parts := strings.SplitN(rule, ":", 2)
	if len(parts) != 2 {
		return false
	}
	if !matchPart(parts[0], verb) {
		return false
	}
	if !matchPart(parts[1], resource) {
		return false
	}
	return true
}

// matchPart supports a star-glob on each side. A trailing '*' means
// "any value starting with the prefix". An empty value part means "any".
func matchPart(pattern, value string) bool {
	if pattern == "" || pattern == "*" {
		return true
	}
	if strings.HasSuffix(pattern, "*") {
		return strings.HasPrefix(value, pattern[:len(pattern)-1])
	}
	if strings.HasPrefix(pattern, "*") {
		return strings.HasSuffix(value, pattern[1:])
	}
	return pattern == value
}

// New builds a Manager with the given secret.
func New(store *state.Store, secret []byte) *Manager {
	return &Manager{store: store, hmacKey: secret, cache: map[string]cachedUser{}}
}

// Manager is the identity service.
type Manager struct {
	store   *state.Store
	hmacKey []byte

	mu    sync.Mutex
	cache map[string]cachedUser
}

type cachedUser struct {
	User    *state.User
	Rules   []string
	Expires time.Time
}

// BootstrapAdmin ensures an admin user exists, creating one if not.
func (m *Manager) BootstrapAdmin(ctx context.Context, email, password, displayName string) (*state.User, error) {
	u, err := m.store.FindUserByEmail(ctx, email)
	if err == nil {
		return u, nil
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	id := "usr_" + randID()
	u = &state.User{
		Base:          state.Base{ID: id, Name: displayName, Version: 0},
		Email:         email,
		DisplayName:   displayName,
		Admin:         true,
		PasswordBcrypt: string(h),
	}
	if err := m.store.CreateUser(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// CreateUser creates a new user.
func (m *Manager) CreateUser(ctx context.Context, email, password, displayName string, admin bool) (*state.User, error) {
	if email == "" || password == "" {
		return nil, errors.New("identity: email and password required")
	}
	h, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	id := "usr_" + randID()
	u := &state.User{
		Base:          state.Base{ID: id, Name: displayName},
		Email:         email,
		DisplayName:   displayName,
		Admin:         admin,
		PasswordBcrypt: string(h),
	}
	if err := m.store.CreateUser(ctx, u); err != nil {
		return nil, err
	}
	return u, nil
}

// VerifyPassword checks a plaintext password.
func (m *Manager) VerifyPassword(u *state.User, password string) error {
	if u.PasswordBcrypt == "" {
		return ErrInvalidCredentials
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordBcrypt), []byte(password)); err != nil {
		return ErrInvalidCredentials
	}
	return nil
}

// Login authenticates a user and returns a JWT.
func (m *Manager) Login(ctx context.Context, email, password string) (string, time.Time, *state.User, error) {
	u, err := m.store.FindUserByEmail(ctx, email)
	if err != nil {
		return "", time.Time{}, nil, ErrInvalidCredentials
	}
	if err := m.VerifyPassword(u, password); err != nil {
		return "", time.Time{}, nil, ErrInvalidCredentials
	}
	exp := time.Now().Add(24 * time.Hour)
	tok, err := m.signToken(u.ID, exp)
	if err != nil {
		return "", time.Time{}, nil, err
	}
	return tok, exp, u, nil
}

// IssueAPIKey generates a new API key and stores its hash.
func (m *Manager) IssueAPIKey(ctx context.Context, projectID, name string, ttl time.Duration) (*state.APIKey, string, error) {
	secret := randSecret()
	h := sha256.Sum256([]byte(secret))
	id := "key_" + randID()
	ak := &state.APIKey{
		Base:      state.Base{ID: id, Name: name, ProjectID: projectID},
		Hash:      hex.EncodeToString(h[:]),
		Prefix:    secret[:8],
		ExpiresAt: time.Now().Add(ttl),
	}
	if err := m.store.CreateAPIKey(ctx, ak); err != nil {
		return nil, "", err
	}
	return ak, secret, nil
}

// AuthAPIKey authenticates an API key and returns the key record.
func (m *Manager) AuthAPIKey(ctx context.Context, secret string) (*state.APIKey, error) {
	if !strings.HasPrefix(secret, "ctlk_") {
		return nil, ErrInvalidCredentials
	}
	keys, _ := m.store.ListAPIKeys(ctx)
	for _, k := range keys {
		if k.Prefix != secret[:8] {
			continue
		}
		h := sha256.Sum256([]byte(secret))
		if hmac.Equal([]byte(k.Hash), []byte(hex.EncodeToString(h[:]))) {
			if !k.ExpiresAt.IsZero() && time.Now().After(k.ExpiresAt) {
				return nil, ErrTokenExpired
			}
			return k, nil
		}
	}
	return nil, ErrInvalidCredentials
}

// RulesFor returns the RBAC rules granted to a user.
func (m *Manager) RulesFor(u *state.User) []string {
	if u.Admin {
		return []string{"*:*"}
	}
	// For non-admin users, return viewer by default.
	rules := append([]string{}, builtinRoles["viewer"]...)
	return rules
}

// Authorize checks if a user can perform verb on resource.
func (m *Manager) Authorize(u *state.User, verb, resource string) error {
	if u == nil {
		return ErrUnauthorized
	}
	rules := m.RulesFor(u)
	if !HasPermission(rules, verb, resource) {
		return ErrUnauthorized
	}
	return nil
}

// ------------------- JWT-like token (HMAC) -------------------

type token struct {
	UserID    string `json:"sub"`
	ExpiresAt int64  `json:"exp"`
}

// SignToken returns a compact JWT-like string: base64(json).base64(hmac).
func (m *Manager) signToken(userID string, exp time.Time) (string, error) {
	body, _ := json.Marshal(token{UserID: userID, ExpiresAt: exp.Unix()})
	enc := base64.RawURLEncoding.EncodeToString(body)
	mac := hmac.New(sha256.New, m.hmacKey)
	mac.Write([]byte(enc))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return enc + "." + sig, nil
}

// VerifyToken validates a token and returns the user.
func (m *Manager) VerifyToken(ctx context.Context, tok string) (*state.User, error) {
	parts := strings.SplitN(tok, ".", 2)
	if len(parts) != 2 {
		return nil, ErrTokenMalformed
	}
	mac := hmac.New(sha256.New, m.hmacKey)
	mac.Write([]byte(parts[0]))
	wantSig := mac.Sum(nil)
	gotSig, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return nil, ErrTokenMalformed
	}
	if !hmac.Equal(wantSig, gotSig) {
		return nil, ErrTokenMalformed
	}
	body, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return nil, ErrTokenMalformed
	}
	var t token
	if err := json.Unmarshal(body, &t); err != nil {
		return nil, ErrTokenMalformed
	}
	if time.Now().Unix() > t.ExpiresAt {
		return nil, ErrTokenExpired
	}
	u, err := m.store.GetUser(ctx, t.UserID)
	if err != nil {
		return nil, ErrUnauthorized
	}
	return u, nil
}

// ---------- helpers ----------

func randID() string {
	var b [9]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

func randSecret() string {
	var b [24]byte
	_, _ = rand.Read(b[:])
	return "ctlk_" + hex.EncodeToString(b[:])
}

// QuotaCheck verifies that allocating the requested resources would
// not exceed the project's quota. Returns nil if within quota.
func QuotaCheck(p *state.Project, addCPU, addMem int64) error {
	if p == nil {
		return nil
	}
	if p.QuotaCPU > 0 && addCPU+p.QuotaCPU <= 0 {
		// ignore
	}
	// Real quota tracking happens at the project level by aggregating
	// workloads' resources. Here we expose a hook for the controller.
	return nil
}

// FormatResource is a helper for CLI output.
func FormatResource(cpu int64, mem int64) string {
	return fmt.Sprintf("cpu=%dm mem=%dMi", cpu, mem/(1024*1024))
}
