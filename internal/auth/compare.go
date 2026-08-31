// Package auth holds the shared credential-comparison primitive used by
// every API-key check in iplayer-arr (dashboard, Newznab and SABnzbd
// surfaces). Keeping one implementation means a hardening change lands
// everywhere at once instead of drifting per package.
package auth

import "crypto/subtle"

// SecretsEqual reports whether the caller-supplied credential matches the
// stored one, using a comparison whose running time does not depend on how
// many leading bytes match. A plain string == returns early at the first
// differing byte, which lets an attacker recover a secret byte by byte from
// response timing.
//
// subtle.ConstantTimeCompare returns 0 for inputs of unequal length, so a
// wrong-length guess is rejected without leaking anything beyond the length
// itself. An empty stored secret is never a match: callers that treat an
// unseeded key specially must decide that before calling here.
func SecretsEqual(given, stored string) bool {
	if stored == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(given), []byte(stored)) == 1
}
