// Package shortcode generates short codes for URLs.
//
// This is a direct port of the original utils.py: a random 6-character
// alphanumeric string. It's kept as-is (rather than jumping straight to
// Snowflake IDs) so this port is a faithful 1:1 translation first -
// swap GenerateShortCode's implementation for a Snowflake-based
// generator later when the project moves to the distributed design
// described in SYSTEM-DESIGN.md, without touching any caller.
package shortcode

import (
	"crypto/rand"
	"math/big"
)

const (
	charset = "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz0123456789"
	length  = 6
)

// GenerateShortCode returns a random 6-character alphanumeric string.
//
// Uses crypto/rand rather than math/rand (the Python original uses
// math's `random`, which is also not cryptographically secure) - this
// is a small upgrade in collision/predictability safety that costs
// nothing, since crypto/rand is standard library.
func GenerateShortCode() (string, error) {
	b := make([]byte, length)
	max := big.NewInt(int64(len(charset)))

	for i := range b {
		n, err := rand.Int(rand.Reader, max)
		if err != nil {
			return "", err
		}
		b[i] = charset[n.Int64()]
	}
	return string(b), nil
}
