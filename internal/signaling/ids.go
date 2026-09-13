package signaling

import "crypto/rand"

// alphabet excludes visually ambiguous characters (0/O, 1/I/L) so a room code
// read aloud or typed by a friend is hard to get wrong.
const alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// randomCode returns an n-character code drawn from alphabet. It panics only if
// the OS entropy source fails, which is not a recoverable condition.
func randomCode(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("signaling: out of entropy: " + err.Error())
	}
	for i := range b {
		b[i] = alphabet[int(b[i])%len(alphabet)]
	}
	return string(b)
}
