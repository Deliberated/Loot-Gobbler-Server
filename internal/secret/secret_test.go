package secret

import (
	"strings"
	"testing"
)

func TestPasswordRoundTrip(t *testing.T) {
	hash := HashPassword("correct horse")
	if !strings.HasPrefix(hash, "$argon2id$v=19$") {
		t.Fatalf("unexpected hash format %q", hash)
	}
	if !VerifyPassword("correct horse", hash) {
		t.Fatal("right password rejected")
	}
	if VerifyPassword("wrong horse", hash) || VerifyPassword("correct horse", "garbage") {
		t.Fatal("wrong password or broken hash accepted")
	}
}

func TestRoleTokenNormalization(t *testing.T) {
	token := NewRoleToken(ModeratorTokenPrefix)
	canonical, ok := NormalizeRoleToken(token)
	if !ok || strings.Contains(canonical, "-") || len(canonical) != 35 {
		t.Fatalf("token %q normalized to %q, %v", token, canonical, ok)
	}
	// Typed sloppily: lower case, spaces instead of dashes, O for 0.
	sloppy := strings.ToLower(strings.ReplaceAll(strings.ReplaceAll(token, "-", " "), "0", "o"))
	if again, ok := NormalizeRoleToken(sloppy); !ok || again != canonical {
		t.Fatalf("sloppy %q normalized to %q, want %q", sloppy, again, canonical)
	}
	for _, bad := range []string{"", "LGX-0000", token + "0", "LGM-UUUU-0000-0000-0000-0000-0000-0000-0000"} {
		if _, ok := NormalizeRoleToken(bad); ok {
			t.Errorf("%q should be rejected", bad)
		}
	}
}
