package service

// shouldWipeGraphForRematerialize reports whether the knowledge graph namespace
// must be cleared before postprocess graph tasks rematerialize. Matches the
// policy in processChunks: wipe on true full rebuild or when stale chunks
// were deleted (orphans would otherwise remain because the engine has no
// per-chunk delete API).
func shouldWipeGraphForRematerialize(reusedChunkCount, staleDeleteCount int) bool {
	return reusedChunkCount == 0 || staleDeleteCount > 0
}
