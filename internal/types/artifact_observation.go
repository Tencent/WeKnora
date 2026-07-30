package types

type ArtifactCacheEvent string

const (
	ArtifactCacheHit           ArtifactCacheEvent = "hit"
	ArtifactCacheMiss          ArtifactCacheEvent = "miss"
	ArtifactCacheClaimed       ArtifactCacheEvent = "claimed"
	ArtifactCacheBusy          ArtifactCacheEvent = "busy"
	ArtifactCacheComputed      ArtifactCacheEvent = "computed"
	ArtifactCacheFailed        ArtifactCacheEvent = "failed"
	ArtifactCacheLeaseTakeover ArtifactCacheEvent = "lease_takeover"
)

// ArtifactObservation maps cache infrastructure events onto PR1's ingestion
// vocabulary without claiming provider requests or attaching payload data.
func ArtifactObservation(operation IngestionOperation, kind, inputPrefix string, event ArtifactCacheEvent) IngestionOperationObservation {
	o := IngestionOperationObservation{Operation: operation, OperationCount: 1, ArtifactKind: kind, InputDigestPrefix: inputPrefix, Success: event != ArtifactCacheFailed}
	o.ArtifactCacheEvent = string(event)
	switch event {
	case ArtifactCacheHit:
		o.CacheStatus, o.ReusedItems = IngestionCacheStatusHit, 1
	case ArtifactCacheComputed:
		o.CacheStatus, o.ComputedItems = IngestionCacheStatusMiss, 1
	case ArtifactCacheFailed:
		o.CacheStatus, o.ErrorCode = IngestionCacheStatusError, "artifact_failed"
	default:
		o.CacheStatus = IngestionCacheStatusMiss
	}
	return o
}
