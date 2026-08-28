// Package fuzzy provides token-substring, case-insensitive matching used for
// fuzzy tool and user name lookups. A query matches a target when every word
// (token) of the query appears as a substring of the target, in any order,
// ignoring letter case. For example, "snow blower" matches "Ego Snow Blower".
package fuzzy

import "strings"

// Match reports whether query fuzzy-matches target using token-substring,
// case-insensitive matching. An empty query returns true only if target is
// empty; a non-empty query must have every token present as a substring of
// target (order-independent).
func Match(query, target string) bool {
	query = strings.TrimSpace(strings.ToLower(query))
	target = strings.ToLower(target)

	if query == "" {
		return strings.TrimSpace(target) == ""
	}

	for _, token := range strings.Fields(query) {
		if token == "" {
			continue
		}
		if !strings.Contains(target, token) {
			return false
		}
	}
	return true
}
