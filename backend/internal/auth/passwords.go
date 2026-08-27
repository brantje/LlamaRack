package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"golang.org/x/crypto/argon2"
)

func validatePassword(password string) error {
	if len(password) < passwordMinLength {
		return fmt.Errorf("password must be at least %d characters", passwordMinLength)
	}
	return nil
}

func hashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash := argon2.IDKey([]byte(password), salt, argonTime, argonMemory, argonThreads, argonKeyLength)
	return fmt.Sprintf("argon2id$v=19$m=%d,t=%d,p=%d$%s$%s", argonMemory, argonTime, argonThreads, base64.RawStdEncoding.EncodeToString(salt), base64.RawStdEncoding.EncodeToString(hash)), nil
}

func verifyPassword(password, encoded string) bool {
	memory, iterations, threads, salt, expected, ok := parsePasswordHash(encoded)
	if !ok {
		return false
	}
	actual := argon2.IDKey([]byte(password), salt, iterations, memory, threads, uint32(len(expected)))
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

func passwordNeedsRehash(encoded string) bool {
	memory, iterations, threads, _, expected, ok := parsePasswordHash(encoded)
	return !ok || memory != argonMemory || iterations != argonTime || threads != argonThreads || len(expected) != argonKeyLength
}

func parsePasswordHash(encoded string) (uint32, uint32, uint8, []byte, []byte, bool) {
	parts := strings.Split(encoded, "$")
	if len(parts) != 5 || parts[0] != "argon2id" || parts[1] != "v=19" {
		return 0, 0, 0, nil, nil, false
	}
	params := strings.Split(parts[2], ",")
	if len(params) != 3 {
		return 0, 0, 0, nil, nil, false
	}
	memory, memoryErr := strconv.ParseUint(strings.TrimPrefix(params[0], "m="), 10, 32)
	iterations, iterationsErr := strconv.ParseUint(strings.TrimPrefix(params[1], "t="), 10, 32)
	threads, threadsErr := strconv.ParseUint(strings.TrimPrefix(params[2], "p="), 10, 8)
	if memoryErr != nil || iterationsErr != nil || threadsErr != nil || iterations < 1 || threads < 1 || memory < 8*threads {
		return 0, 0, 0, nil, nil, false
	}
	salt, saltErr := base64.RawStdEncoding.DecodeString(parts[3])
	expected, expectedErr := base64.RawStdEncoding.DecodeString(parts[4])
	if saltErr != nil || expectedErr != nil || len(salt) == 0 || len(expected) == 0 {
		return 0, 0, 0, nil, nil, false
	}
	return uint32(memory), uint32(iterations), uint8(threads), salt, expected, true
}

func randomToken(length int) (string, error) {
	if length <= 0 {
		return "", errors.New("token length must be positive")
	}
	buffer := make([]byte, length)
	if _, err := rand.Read(buffer); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(buffer), nil
}

func tokenHash(token string) string {
	sum := sha256.Sum256([]byte(token))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func truncate(value string, max int) string {
	if len(value) <= max {
		return value
	}
	return value[:max]
}
