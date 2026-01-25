// Package callback provides drift notification webhook callback functionality.
package callback

import (
	"crypto/sha256"
	"encoding/hex"
	"hash"

	"github.com/kausality-io/kausality/pkg/callback/v1alpha1"
)

// GenerateDriftID generates a stable ID from drift context for deduplication.
// The ID is a 16-character hex string derived from the parent reference,
// child reference, and spec diff hash.
func GenerateDriftID(parent, child v1alpha1.ObjectReference, specDiff []byte) string {
	h := sha256.New()
	hashObjectRef(h, parent)
	hashObjectRef(h, child)
	h.Write(specDiff)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// GenerateResolutionID generates an ID for a resolved drift notification.
// It uses only the parent and child references since the diff is no longer relevant.
func GenerateResolutionID(parent, child v1alpha1.ObjectReference) string {
	h := sha256.New()
	hashObjectRef(h, parent)
	hashObjectRef(h, child)
	return hex.EncodeToString(h.Sum(nil))[:16]
}

// hashObjectRef writes an object reference to a hash with null-byte separators.
func hashObjectRef(h hash.Hash, ref v1alpha1.ObjectReference) {
	for _, field := range []string{ref.APIVersion, ref.Kind, ref.Namespace, ref.Name} {
		h.Write([]byte(field))
		h.Write([]byte{0})
	}
}
