// Package secret hashes passwords and creates and hashes tokens.
package secret

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base32"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"

	"golang.org/x/crypto/argon2"
)

// Argon2id parameters recommended by OWASP (19 MiB, 2 passes, 1 lane).
const (
	argonMemoryKiB = 19 * 1024
	argonTime      = 2
	argonThreads   = 1
	argonKeyLen    = 32
)

// HashPassword returns a PHC-style Argon2id string ($argon2id$v=19$m=...,t=...,p=...$salt$hash).
func HashPassword(password string) string {
	salt := randomBytes(16)
	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemoryKiB, argonThreads, argonKeyLen)
	b64 := base64.RawStdEncoding
	return fmt.Sprintf("$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, argonMemoryKiB, argonTime, argonThreads, b64.EncodeToString(salt), b64.EncodeToString(key))
}

// VerifyPassword reports whether password matches a hash made by HashPassword.
func VerifyPassword(password, encoded string) bool {
	parts := strings.Split(encoded, "$")
	if len(parts) != 6 || parts[1] != "argon2id" {
		return false
	}
	var version int
	var memory, time uint32
	var threads uint8
	if _, err := fmt.Sscanf(parts[2], "v=%d", &version); err != nil || version != argon2.Version {
		return false
	}
	if _, err := fmt.Sscanf(parts[3], "m=%d,t=%d,p=%d", &memory, &time, &threads); err != nil {
		return false
	}
	b64 := base64.RawStdEncoding
	salt, err1 := b64.DecodeString(parts[4])
	want, err2 := b64.DecodeString(parts[5])
	if err1 != nil || err2 != nil || len(want) == 0 {
		return false
	}
	got := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(want)))
	return subtle.ConstantTimeCompare(got, want) == 1
}

// NewSessionToken returns a random bearer token for a logged-in client.
func NewSessionToken() string {
	return "lgs_" + base64.RawURLEncoding.EncodeToString(randomBytes(32))
}

// Role token prefixes: LGA = admin claim (printed in the console), LGM = moderator invite (made by an admin).
const (
	AdminTokenPrefix     = "LGA"
	ModeratorTokenPrefix = "LGM"
)

// Crockford's base32: no I, L, O or U, so tokens survive being read aloud or typed by hand.
const crockford = "0123456789ABCDEFGHJKMNPQRSTVWXYZ"

var crockfordEncoding = base32.NewEncoding(crockford).WithPadding(base32.NoPadding)

// NewRoleToken returns a token like LGA-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX-XXXX (160 random bits).
func NewRoleToken(prefix string) string {
	body := crockfordEncoding.EncodeToString(randomBytes(20)) // 20 bytes = exactly 32 characters
	groups := []string{prefix}
	for i := 0; i < len(body); i += 4 {
		groups = append(groups, body[i:i+4])
	}
	return strings.Join(groups, "-")
}

// NormalizeRoleToken turns user input into the canonical form that is hashed and stored. It ignores case, dashes
// and spaces and reads O as 0 and I/L as 1. ok is false if the input cannot be a role token.
func NormalizeRoleToken(input string) (canonical string, ok bool) {
	s := strings.ToUpper(strings.NewReplacer("-", "", " ", "", "\t", "").Replace(strings.TrimSpace(input)))
	if len(s) != 3+32 || (s[:3] != AdminTokenPrefix && s[:3] != ModeratorTokenPrefix) {
		return "", false
	}
	body := strings.NewReplacer("O", "0", "I", "1", "L", "1").Replace(s[3:])
	for _, r := range body {
		if !strings.ContainsRune(crockford, r) {
			return "", false
		}
	}
	return s[:3] + body, true
}

// ResetCodePrefix starts a password reset code (LGR-XXXX-XXXX-XXXX), made for a player who forgot their password.
const ResetCodePrefix = "LGR"

const resetCodeChars = 12 // 60 random bits: short enough to pass on by hand; single use, bound to one IGN, expiring

// NewResetCode returns a password reset code like LGR-XXXX-XXXX-XXXX.
func NewResetCode() string {
	body := crockfordEncoding.EncodeToString(randomBytes(8))[:resetCodeChars]
	return ResetCodePrefix + "-" + body[0:4] + "-" + body[4:8] + "-" + body[8:12]
}

// NormalizeResetCode turns user input into the canonical form that is hashed, read as loosely as role tokens (case,
// dashes and spaces ignored, O as 0, I/L as 1). The prefix may be left out. ok is false if it cannot be a code.
func NormalizeResetCode(input string) (canonical string, ok bool) {
	s := strings.ToUpper(strings.NewReplacer("-", "", " ", "", "\t", "").Replace(strings.TrimSpace(input)))
	s = strings.TrimPrefix(s, ResetCodePrefix)
	if len(s) != resetCodeChars {
		return "", false
	}
	body := strings.NewReplacer("O", "0", "I", "1", "L", "1").Replace(s)
	for _, r := range body {
		if !strings.ContainsRune(crockford, r) {
			return "", false
		}
	}
	return ResetCodePrefix + body, true
}

// HashToken returns the SHA-256 hex digest under which a token is stored. Tokens are long and random, so a fast
// hash is enough; the database never holds a usable token.
func HashToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("crypto/rand failed: " + err.Error())
	}
	return b
}
