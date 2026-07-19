// Package auth holds password hashing shared by the API login handler (AUTH-1)
// and the invite-only user-create CLI (AUTH-2). Keeping it in its own package
// lets both callers depend on it without importing each other.
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

// argon2id parameters (design 07 "Auth and accounts"; OWASP second recommended
// config). Low peak memory suits the shared 4 GB VPS and the tiny login volume.
// The params are also encoded into every PHC string, so a future tuning can
// raise them without a data migration — old hashes still verify against their
// own recorded params.
const (
	argonTime    = 2
	argonMemory  = 19456 // KiB (19 MiB)
	argonThreads = 1
	argonSaltLen = 16
	argonKeyLen  = 32

	// maxPasswordLen bounds the argon2 work an attacker can force by sending a
	// huge password. 1024 bytes is far above any real passphrase.
	maxPasswordLen = 1024

	// Stored hashes are trusted only after parsing and bounds checks. These caps
	// keep a malformed-but-parseable database value from panicking argon2 or
	// forcing excessive allocation during login.
	maxPHCHashLen      = 512
	maxVerifyTime      = 10
	maxVerifyMemoryKiB = 256 * 1024
	maxVerifyThreads   = 4
	minSaltLen         = 8
	maxSaltLen         = 64
	minKeyLen          = 16
	maxKeyLen          = 64
)

// ErrPasswordTooLong is returned when a password exceeds maxPasswordLen.
var ErrPasswordTooLong = fmt.Errorf("password exceeds %d bytes", maxPasswordLen)

// DummyHash is a valid PHC argon2id hash of a discarded random password. Login
// verifies against it when the email is unknown or the user has no
// password_hash so the failing path still burns one argon2id computation and
// timing does not reveal whether the account exists. It must never match any
// real password (its plaintext was thrown away).
const DummyHash = "$argon2id$v=19$m=19456,t=2,p=1$ClzmGysxMTp/RFyIazZhUQ$AG2OnfvJYMcvJEC7hyKJpMH8ZCwby9D+K/Mzqb5imbg"

// HashPassword returns a PHC-encoded argon2id hash of password:
//
//	$argon2id$v=19$m=19456,t=2,p=1$<b64salt>$<b64key>
func HashPassword(password string) (string, error) {
	if len(password) > maxPasswordLen {
		return "", ErrPasswordTooLong
	}

	salt := make([]byte, argonSaltLen)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate salt: %w", err)
	}

	key := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLen)
	return encodeHash(salt, key, argonTime, argonMemory, argonThreads), nil
}

// VerifyPassword reports whether password matches the PHC-encoded argon2id
// hash. Parameters (memory/time/threads) are read from the hash string, so a
// hash produced with different params still verifies correctly. Comparison is
// constant-time. A malformed hash returns an error; callers treat that as an
// authentication failure and never surface the detail.
func VerifyPassword(phcHash, password string) (bool, error) {
	if len(password) > maxPasswordLen {
		return false, ErrPasswordTooLong
	}

	salt, key, time, memory, threads, err := decodeHash(phcHash)
	if err != nil {
		return false, err
	}

	computed := argon2.IDKey([]byte(password), salt, time, memory, threads, uint32(len(key)))
	return subtle.ConstantTimeCompare(computed, key) == 1, nil
}

func encodeHash(salt, key []byte, time, memory uint32, threads uint8) string {
	b64 := base64.RawStdEncoding
	return fmt.Sprintf(
		"$argon2id$v=%d$m=%d,t=%d,p=%d$%s$%s",
		argon2.Version, memory, time, threads,
		b64.EncodeToString(salt), b64.EncodeToString(key),
	)
}

func decodeHash(phcHash string) (salt, key []byte, time, memory uint32, threads uint8, err error) {
	if len(phcHash) > maxPHCHashLen {
		return nil, nil, 0, 0, 0, errors.New("argon2id hash is too long")
	}

	parts := strings.Split(phcHash, "$")
	// ["", "argon2id", "v=19", "m=..,t=..,p=..", "<salt>", "<key>"]
	if len(parts) != 6 || parts[0] != "" || parts[1] != "argon2id" {
		return nil, nil, 0, 0, 0, errors.New("malformed argon2id hash")
	}

	version, err := parsePHCUint32(parts[2], "v")
	if err != nil {
		return nil, nil, 0, 0, 0, err
	}
	if version != uint32(argon2.Version) {
		return nil, nil, 0, 0, 0, fmt.Errorf("unsupported argon2 version %d", version)
	}

	memory, time, threads, err = parseArgonParams(parts[3])
	if err != nil {
		return nil, nil, 0, 0, 0, err
	}

	b64 := base64.RawStdEncoding
	salt, err = b64.DecodeString(parts[4])
	if err != nil {
		return nil, nil, 0, 0, 0, fmt.Errorf("decode salt: %w", err)
	}
	key, err = b64.DecodeString(parts[5])
	if err != nil {
		return nil, nil, 0, 0, 0, fmt.Errorf("decode key: %w", err)
	}
	if len(salt) < minSaltLen || len(salt) > maxSaltLen {
		return nil, nil, 0, 0, 0, fmt.Errorf("argon2id salt length %d is outside supported bounds", len(salt))
	}
	if len(key) < minKeyLen || len(key) > maxKeyLen {
		return nil, nil, 0, 0, 0, fmt.Errorf("argon2id key length %d is outside supported bounds", len(key))
	}

	return salt, key, time, memory, threads, nil
}

func parseArgonParams(raw string) (memory, time uint32, threads uint8, err error) {
	parts := strings.Split(raw, ",")
	if len(parts) != 3 {
		return 0, 0, 0, errors.New("malformed argon2 params")
	}

	memory, err = parsePHCUint32(parts[0], "m")
	if err != nil {
		return 0, 0, 0, err
	}
	time, err = parsePHCUint32(parts[1], "t")
	if err != nil {
		return 0, 0, 0, err
	}
	parsedThreads, err := parsePHCUint32(parts[2], "p")
	if err != nil {
		return 0, 0, 0, err
	}
	if parsedThreads > maxVerifyThreads {
		return 0, 0, 0, fmt.Errorf("argon2 threads %d exceeds supported maximum", parsedThreads)
	}
	threads = uint8(parsedThreads)

	if time == 0 || time > maxVerifyTime {
		return 0, 0, 0, fmt.Errorf("argon2 time %d is outside supported bounds", time)
	}
	if memory == 0 || memory > maxVerifyMemoryKiB {
		return 0, 0, 0, fmt.Errorf("argon2 memory %d is outside supported bounds", memory)
	}
	if threads == 0 {
		return 0, 0, 0, errors.New("argon2 threads must be positive")
	}

	return memory, time, threads, nil
}

func parsePHCUint32(raw, key string) (uint32, error) {
	prefix := key + "="
	value, ok := strings.CutPrefix(raw, prefix)
	if !ok || value == "" {
		return 0, fmt.Errorf("malformed argon2 %s field", key)
	}
	parsed, err := strconv.ParseUint(value, 10, 32)
	if err != nil {
		return 0, fmt.Errorf("parse argon2 %s field: %w", key, err)
	}
	return uint32(parsed), nil
}
