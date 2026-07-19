package auth

import (
	"strings"
	"testing"
)

func TestHashPasswordRoundTrip(t *testing.T) {
	hash, err := HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	if !strings.HasPrefix(hash, "$argon2id$v=19$m=19456,t=2,p=1$") {
		t.Fatalf("hash has unexpected PHC prefix: %q", hash)
	}

	ok, err := VerifyPassword(hash, "correct horse battery staple")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if !ok {
		t.Fatal("verify returned false for the correct password")
	}
}

func TestVerifyPasswordRejectsWrongPassword(t *testing.T) {
	hash, err := HashPassword("hunter2")
	if err != nil {
		t.Fatalf("hash password: %v", err)
	}
	ok, err := VerifyPassword(hash, "hunter3")
	if err != nil {
		t.Fatalf("verify: %v", err)
	}
	if ok {
		t.Fatal("verify returned true for a wrong password")
	}
}

func TestHashPasswordUsesRandomSalt(t *testing.T) {
	h1, _ := HashPassword("same")
	h2, _ := HashPassword("same")
	if h1 == h2 {
		t.Fatal("two hashes of the same password are identical; salt is not random")
	}
}

func TestVerifyPasswordMalformedHash(t *testing.T) {
	cases := []string{
		"",
		"not-a-phc-string",
		"$argon2id$v=19$m=19456,t=2,p=1$onlyfourparts",
		"$argon2i$v=19$m=19456,t=2,p=1$ClzmGysxMTp/RFyIazZhUQ$AG2OnfvJYMcvJEC7hyKJpMH8ZCwby9D+K/Mzqb5imbg",
		"$argon2id$v=18$m=19456,t=2,p=1$ClzmGysxMTp/RFyIazZhUQ$AG2OnfvJYMcvJEC7hyKJpMH8ZCwby9D+K/Mzqb5imbg",
		"$argon2id$v=19$notparams$ClzmGysxMTp/RFyIazZhUQ$AG2OnfvJYMcvJEC7hyKJpMH8ZCwby9D+K/Mzqb5imbg",
	}
	for _, c := range cases {
		ok, err := VerifyPassword(c, "whatever")
		if err == nil {
			t.Fatalf("VerifyPassword(%q) err = nil, want error", c)
		}
		if ok {
			t.Fatalf("VerifyPassword(%q) ok = true, want false", c)
		}
	}
}

func TestVerifyPasswordRejectsUnsafePHCParams(t *testing.T) {
	cases := []string{
		strings.Replace(DummyHash, "t=2", "t=0", 1),
		strings.Replace(DummyHash, "m=19456", "m=0", 1),
		strings.Replace(DummyHash, "p=1", "p=0", 1),
		strings.Replace(DummyHash, "t=2", "t=11", 1),
		strings.Replace(DummyHash, "m=19456", "m=262145", 1),
		strings.Replace(DummyHash, "p=1", "p=5", 1),
		strings.Replace(DummyHash, "p=1", "p=1junk", 1),
		encodeHash(make([]byte, argonSaltLen), make([]byte, maxKeyLen+1), argonTime, argonMemory, argonThreads),
		strings.Repeat("a", maxPHCHashLen+1),
	}

	for _, c := range cases {
		ok, err := VerifyPassword(c, "whatever")
		if err == nil {
			t.Fatalf("VerifyPassword(%q) err = nil, want error", c)
		}
		if ok {
			t.Fatalf("VerifyPassword(%q) ok = true, want false", c)
		}
	}
}

// TestVerifyPasswordParamTamperingFailsClosed confirms that altering the
// encoded params (so the recorded key no longer matches a recomputation) makes
// verification return false rather than accidentally succeeding.
func TestVerifyPasswordParamTampering(t *testing.T) {
	hash, err := HashPassword("secret")
	if err != nil {
		t.Fatalf("hash: %v", err)
	}
	tampered := strings.Replace(hash, "t=2", "t=3", 1)
	ok, err := VerifyPassword(tampered, "secret")
	if err != nil {
		t.Fatalf("verify tampered: %v", err)
	}
	if ok {
		t.Fatal("tampered-params hash verified true; want false")
	}
}

func TestDummyHashNeverVerifies(t *testing.T) {
	for _, guess := range []string{"", "password", "admin", "correct horse battery staple"} {
		ok, err := VerifyPassword(DummyHash, guess)
		if err != nil {
			t.Fatalf("verify DummyHash against %q: %v", guess, err)
		}
		if ok {
			t.Fatalf("DummyHash verified true for %q; it must never match", guess)
		}
	}
}

func TestHashPasswordRejectsOverlongInput(t *testing.T) {
	long := strings.Repeat("a", maxPasswordLen+1)
	if _, err := HashPassword(long); err == nil {
		t.Fatal("HashPassword accepted an overlong password; want ErrPasswordTooLong")
	}
	if _, err := VerifyPassword(DummyHash, long); err == nil {
		t.Fatal("VerifyPassword accepted an overlong password; want ErrPasswordTooLong")
	}
}
