package main

import "math/rand/v2"

const shortCodeAlphabet = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

// generateShortCode picks `length` random characters, equivalent to the
// Python version's random.choice over string.ascii_letters + string.digits.
func generateShortCode(length int) string {
	code := make([]byte, length)
	for i := range code {
		code[i] = shortCodeAlphabet[rand.IntN(len(shortCodeAlphabet))]
	}
	return string(code)
}
