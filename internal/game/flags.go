package game

import (
	"crypto/rand"
	"encoding/hex"
)

var flagRandRead = rand.Read

// mintFlag returns a fresh flag of the form PREFIX{RANDOM}.
func mintFlag(prefix string) string {
	var b [16]byte
	if _, err := flagRandRead(b[:]); err != nil {
		panic(err)
	}
	return prefix + "{" + hex.EncodeToString(b[:]) + "}"
}
