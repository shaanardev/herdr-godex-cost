package codex

import "regexp"

var FullUUIDPattern = regexp.MustCompile(`(?i)[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`)

var TruncatedUUIDPattern = regexp.MustCompile(`(?i)([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{0,11})(…|\.{3})`)

var validUUIDPrefix = regexp.MustCompile(`(?i)^[0-9a-f]{8}(?:-[0-9a-f]{1,4}){0,3}(?:-[0-9a-f]{0,11})?$`)

var validSessionID = regexp.MustCompile(`^[A-Za-z0-9-]+$`)

// Reports whether an ID is safe to use as a Codex thread ID.
func ValidSessionID(id string) bool {
	return validSessionID.MatchString(id)
}
