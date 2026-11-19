package hasher

import "testing"

func TestPasswordHasher(t *testing.T) {
	hasher := NewPasswordHasher(nil)
	pwd := "secure-pass-1234"

	hash, err := hasher.HashPassword(pwd)
	if err != nil {
		t.Fatalf("unexpected hash error: %v", err)
	}

	valid, err := hasher.VerifyPassword(pwd, hash)
	if err != nil || !valid {
		t.Fatalf("expected verification to pass, valid=%v, err=%v", valid, err)
	}

	valid, err = hasher.VerifyPassword("wrong-pass", hash)
	if err != nil || valid {
		t.Fatalf("expected wrong password verification to fail, valid=%v, err=%v", valid, err)
	}
}
