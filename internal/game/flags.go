package game

import (
	"crypto/rand"
	"encoding/base32"
)

var flagEncoding = base32.StdEncoding.WithPadding(base32.NoPadding)

// mintFlag returns a fresh flag of the form PREFIX{RANDOM}.
func mintFlag(prefix string) string {
	var b [20]byte
	rand.Read(b[:])
	return prefix + "{" + flagEncoding.EncodeToString(b[:]) + "}"
}
