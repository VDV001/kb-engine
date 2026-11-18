package blobs3

// SetMaxPurgeRounds lowers the round limit for a test; the returned func
// restores it.
func SetMaxPurgeRounds(n int) (restore func()) {
	prev := maxPurgeRounds
	maxPurgeRounds = n
	return func() { maxPurgeRounds = prev }
}
