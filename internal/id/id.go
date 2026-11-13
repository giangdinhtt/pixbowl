package id

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"regexp"
)

const alphabet = "0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz"

// pattern is deliberately strict: it is the only thing standing between a URL
// path and the storage layer, so anything that is not base62 is rejected.
var pattern = regexp.MustCompile(`^[0-9A-Za-z]{6,64}$`)

// New returns a cryptographically random base62 identifier.
// 12 characters carry ~71 bits of entropy, which makes the link itself the
// only practical way to discover an image.
func New(length int) (string, error) {
	if length <= 0 {
		length = 12
	}
	out := make([]byte, length)
	buf := make([]byte, length*2)
	for i := 0; i < length; {
		if _, err := rand.Read(buf); err != nil {
			return "", err
		}
		for _, b := range buf {
			// Rejection sampling keeps the distribution uniform (256 % 62 != 0).
			if b >= 248 {
				continue
			}
			out[i] = alphabet[int(b)%62]
			i++
			if i == length {
				break
			}
		}
	}
	return string(out), nil
}

// Token returns the secret handed to the uploader so they can delete their own
// image later. Only its SHA-256 hash is stored.
func Token() (string, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func Valid(value string) bool {
	return pattern.MatchString(value)
}

// Equal compares two secrets in constant time.
func Equal(a, b string) bool {
	return subtle.ConstantTimeCompare([]byte(a), []byte(b)) == 1
}
