// Package callback provides drift notification webhook callback functionality.
package callback

import (
	"crypto/sha256"
	"encoding/hex"

	"github.com/kausality-io/kausality/pkg/callback/v1alpha1"
)

// GenerateDriftID generates a stable ID from drift context for deduplication.
// The ID is a 16-character hex string derived from the parent reference,
// child reference, and spec diff hash.
func GenerateDriftID(parent, child v1alpha1.ObjectReference, specDiff []byte) string {
	h := sha256.New()

	// Hash parent reference
	h.Write([]byte(parent.APIVersion))
	h.Write([]byte{0}) // separator
	h.Write([]byte(parent.Kind))
	h.Write([]byte{0})
	h.Write([]byte(parent.Namespace))
	h.Write([]byte{0})
	h.Write([]byte(parent.Name))
	h.Write([]byte{0})

	// Hash child reference
	h.Write([]byte(child.APIVersion))
	h.Write([]byte{0})
	h.Write([]byte(child.Kind))
	h.Write([]byte{0})
	h.Write([]byte(child.Namespace))
	h.Write([]byte{0})
	h.Write([]byte(child.Name))
	h.Write([]byte{0})

	// Hash spec diff
	h.Write(specDiff)

	return hex.EncodeToString(h.Sum(nil))[:16]
}

// GenerateResolutionID generates an ID for a resolved drift notification.
// It uses only the parent and child references since the diff is no longer relevant.
func GenerateResolutionID(parent, child v1alpha1.ObjectReference) string {
	h := sha256.New()

	// Hash parent reference
	h.Write([]byte(parent.APIVersion))
	h.Write([]byte{0})
	h.Write([]byte(parent.Kind))
	h.Write([]byte{0})
	h.Write([]byte(parent.Namespace))
	h.Write([]byte{0})
	h.Write([]byte(parent.Name))
	h.Write([]byte{0})

	// Hash child reference
	h.Write([]byte(child.APIVersion))
	h.Write([]byte{0})
	h.Write([]byte(child.Kind))
	h.Write([]byte{0})
	h.Write([]byte(child.Namespace))
	h.Write([]byte{0})
	h.Write([]byte(child.Name))

	return hex.EncodeToString(h.Sum(nil))[:16]
}
