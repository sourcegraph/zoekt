package languages

import "strings"

// This file contains private functions
// vendored from the go-enry codebase.

// convertToAliasKey is vendored from go-enry to make sure
// we're normalizing strings the same way.
func convertToAliasKey(langName string) string {
	ak, _, _ := strings.Cut(langName, `,`)
	ak = strings.ReplaceAll(ak, ` `, `_`)
	ak = strings.ToLower(ak)
	return ak
}
