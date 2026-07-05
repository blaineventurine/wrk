package placeholders

import "strings"

// Expand replaces supported placeholders in a string.
//
// Unknown placeholders are left unchanged.
func Expand(value string, ctx Context) string {
	replacer := strings.NewReplacer(
		"{root}", ctx.Root,
		"{parent}", ctx.Parent,
		"{match}", ctx.Match,
		"{shared}", ctx.Shared,
	)

	return replacer.Replace(value)
}
