package im

// ResolveMode returns channel.Mode, falling back to def when it is empty.
func ResolveMode(channel *IMChannel, def string) string {
	if channel.Mode == "" {
		return def
	}
	return channel.Mode
}
