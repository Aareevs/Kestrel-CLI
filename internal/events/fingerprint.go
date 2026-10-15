package events

import (
	"hash/fnv"
	"regexp"
	"strconv"
	"strings"
)

var (
	fpTimestampRe = regexp.MustCompile(`\d{4}-\d{2}-\d{2}[T ]\d{2}:\d{2}:\d{2}(?:[.,]\d+)?(?:Z|[+-]\d{2}:?\d{2})?|\b\d{2}:\d{2}:\d{2}(?:[.,]\d+)?\b`)
	fpUUIDRe      = regexp.MustCompile(`(?i)\b[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}\b`)
	fpHexRe       = regexp.MustCompile(`(?i)\b0x[0-9a-f]+\b|\b[0-9a-f]{12,}\b`)
	fpNumberRe    = regexp.MustCompile(`\d+`)
	fpSpaceRe     = regexp.MustCompile(`\s+`)
)

// Normalize reduces a message to its shape: timestamps, UUIDs, hex IDs and
// numbers are replaced so that "retry 3 of 5 (id=af93…)" and
// "retry 4 of 5 (id=0b12…)" compare equal.
func Normalize(msg string) string {
	s := fpTimestampRe.ReplaceAllString(msg, "<ts>")
	s = fpUUIDRe.ReplaceAllString(s, "<uuid>")
	s = fpHexRe.ReplaceAllString(s, "<hex>")
	s = fpNumberRe.ReplaceAllString(s, "<n>")
	s = fpSpaceRe.ReplaceAllString(strings.TrimSpace(s), " ")
	return s
}

// Fingerprint hashes the normalized message.
func Fingerprint(msg string) string {
	h := fnv.New64a()
	h.Write([]byte(Normalize(msg)))
	return strconv.FormatUint(h.Sum64(), 16)
}
