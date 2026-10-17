// Package ref parses the "@"-separated artifact references shared by the
// capability runtime and provider command surfaces ("name@version"). It exists
// so the trimming and empty-name handling are implemented once instead of
// being re-spelled in each CLI command package.
package ref

import "strings"

// Parse splits "name@version" into its trimmed components. version is empty
// when no "@" is present. ok is false when name is empty after trimming.
//
// The name is deliberately not validated here: capability runtime names
// ("owner:name") and provider names ("process") have different syntax rules,
// enforced by their respective validators. Parse only owns the shape both
// surfaces share.
func Parse(s string) (name, version string, ok bool) {
	name, version, _ = strings.Cut(strings.TrimSpace(s), "@")
	name = strings.TrimSpace(name)
	version = strings.TrimSpace(version)
	return name, version, name != ""
}
