package location

// SharedLocation describes the shared storage location for a resource.
type SharedLocation struct {
	Path string

	// Empty if the resource is not fingerprinted.
	Fingerprint string
}
