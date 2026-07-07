package config

// Origin identifies where a resource definition came from.
type Origin string

const (
	// OriginShared means the resource is defined in the committed
	// .wrk.yml file.
	OriginShared Origin = "shared"

	// OriginLocal means the resource is defined only in the local
	// (uncommitted) .wrk.local.yml file.
	OriginLocal Origin = "local"

	// OriginLocalOverride means the resource is defined in shared config
	// but overridden by a same-named entry in local config.
	OriginLocalOverride Origin = "local-override"
)
