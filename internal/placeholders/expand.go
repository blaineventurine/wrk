package placeholders

import (
	"fmt"
	"regexp"
	"strings"
)

// unknownPlaceholder matches any remaining `{lower-case-word}` sequence
// after replacement. If any match survives, the user typed a placeholder
// name we do not recognize (typically a typo like `{shred}` for `{shared}`).
var unknownPlaceholder = regexp.MustCompile(`\{[a-z]+\}`)

// Expand replaces supported placeholders in a string.
//
// Unknown placeholders are left unchanged. Prefer ExpandStrict at any
// callsite where an unknown placeholder is more likely a user typo than
// intentional literal text.
func Expand(value string, ctx Context) string {
	return replacer(ctx).Replace(value)
}

// ExpandStrict replaces supported placeholders in a string and returns
// an error if any unknown `{word}` sequence remains after replacement.
//
// Use this for user-controlled inputs where a typo (e.g. `{shred}` for
// `{shared}`) would otherwise silently pass through as a literal path
// segment.
func ExpandStrict(value string, ctx Context) (string, error) {
	expanded := replacer(ctx).Replace(value)

	matches := unknownPlaceholder.FindAllString(expanded, -1)
	if len(matches) == 0 {
		return expanded, nil
	}

	// Deduplicate while preserving first-seen order so the error message
	// is stable regardless of how many times a typo appears.
	seen := make(map[string]bool, len(matches))
	unique := matches[:0]
	for _, m := range matches {
		if seen[m] {
			continue
		}
		seen[m] = true
		unique = append(unique, m)
	}

	return "", fmt.Errorf(
		"unknown placeholder(s) %s in %q",
		strings.Join(unique, ", "),
		value,
	)
}

func replacer(ctx Context) *strings.Replacer {
	return strings.NewReplacer(
		"{root}", ctx.Root,
		"{parent}", ctx.Parent,
		"{match}", ctx.Match,
		"{shared}", ctx.Shared,
	)
}
