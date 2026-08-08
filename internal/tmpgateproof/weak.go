// Package tmpgateproof is a THROWAWAY package. It exists only to prove that
// the CI security-gate really goes red when a security scan finds something.
// It is deleted again later on this same branch and must never reach main.
//
// The MD5 call below is deliberate. gosec reports it as G401 (weak
// cryptographic primitive) and G505 (blocklisted import crypto/md5). There is
// no #nosec comment on purpose — the whole point is to let gosec fail.
package tmpgateproof

import (
	"crypto/md5"
	"encoding/hex"
)

// WeakHash hashes data with MD5. Deliberately insecure. Do not use.
func WeakHash(data []byte) string {
	sum := md5.Sum(data)
	return hex.EncodeToString(sum[:])
}
