package api

import (
	"crypto/rand"
	"fmt"
)

// IdempotencyKey overrides the generated keys. Set from --idempotency-key, for
// retrying a create whose first attempt's outcome is unknown (a timeout, a
// dropped connection): rerunning with the same key either replays the stored
// success or re-executes cleanly - it can never double-create.
var IdempotencyKey string

// nextIdempotencyKey returns the key for one logical create: the override when
// set, otherwise a fresh UUID. Fresh per call, not per process: a command that
// creates twice must not reuse a key with a different body which is a 409.
func nextIdempotencyKey() string {
	if IdempotencyKey != "" {
		return IdempotencyKey
	}
	return newUUIDv4()
}

// idempotentRoutes are the seven create routes that read the Idempotency-Key
// header, per the console docs (concepts/idempotency.md). Keys are the
// post-apiPrefix paths that callers pass to Post.
//
// The header is ignored everywhere else, so sending it everywhere would be
// harmless, but keeping the list here means a new create command gets replay
// protection without its author asking, and --idempotency-key's help text stays
// honest. Deliberately absent: create-by-file (multipart) does not participate;
// only create-by-upload does.
var idempotentRoutes = map[string]bool{
	"/analyses/audiences/create":                 true,
	"/analyses/audiences/create-lookalike":       true,
	"/analyses/cohorts/create":                   true,
	"/analyses/activations/create":               true,
	"/analyses/projects/create":                  true,
	"/analyses/schedules/create":                 true,
	"/my-data/pois/submissions/create-by-upload": true,
}

func isIdempotent(path string) bool { return idempotentRoutes[path] }

// newUUIDv4 builds a random UUID without taking on a dependency.
func newUUIDv4() string {
	var b [16]byte
	_, _ = rand.Read(b[:])      // never fails as of Go 1.24; panics internally instead
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // RFC 4122 variant
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:16])
}
