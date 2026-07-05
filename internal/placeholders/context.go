package placeholders

// Context contains the values available for placeholder expansion.
type Context struct {
	Root   string
	Parent string
	Match  string
	Shared string
}
