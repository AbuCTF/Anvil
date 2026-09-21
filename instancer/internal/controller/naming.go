package controller

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
)

// InstanceID is the deterministic id for a (team, challenge) pair. Same inputs
// always map to the same instance, so the API can look one up without storing
// the mapping, and hostnames are unguessable without the secret.
func InstanceID(secret, teamID, challengeID string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(teamID))
	m.Write([]byte{0})
	m.Write([]byte(challengeID))
	return hex.EncodeToString(m.Sum(nil))[:16]
}

// namespaceFor is the namespace holding one instance's pods.
func namespaceFor(instanceID string) string {
	return "inst-" + instanceID
}

// host builds a per-instance, per-category player-facing hostname:
//
//	<prefix>-<id>.<category>.<baseDomain>
//
// The prefix disambiguates multi-exposed challenges; the id makes it
// unguessable and unique per team.
func host(prefix, instanceID, category, baseDomain string) string {
	return fmt.Sprintf("%s-%s.%s.%s", prefix, instanceID, category, baseDomain)
}
