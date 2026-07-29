package container

import (
	"context"
	"fmt"

	"github.com/Tencent/WeKnora/internal/artifact"
	"github.com/Tencent/WeKnora/internal/logger"
)

func newArtifactObserver() artifact.Observer {
	return func(event artifact.Event) {
		fields := artifactEventFields(event)
		entry := logger.GetLogger(context.Background()).WithFields(fields)
		switch event.Kind {
		case artifact.EventCorrupt, artifact.EventStoreFailure:
			entry.Warn("processing artifact cache fallback")
		default:
			entry.Debug("processing artifact cache")
		}
	}
}

func artifactEventFields(event artifact.Event) logger.Fields {
	fields := logger.Fields{
		"artifact_stage":        event.Lookup.Stage,
		"cache_outcome":         artifactCacheOutcome(event.Kind),
		"cache_reason":          event.Reason,
		"key_version":           event.Lookup.KeyVersion,
		"output_schema_version": event.OutputSchema,
		"provider_call":         event.ProviderCall,
		"singleflight_wait_ms":  event.SingleflightWaitMS,
	}
	if event.BatchTotal > 0 {
		fields["batch_total"] = event.BatchTotal
		fields["batch_hits"] = event.BatchHits
		fields["batch_misses"] = event.BatchMisses
		fields["batch_deduplicated"] = event.BatchDeduplicated
	}
	if event.Err != nil {
		// Error values from database drivers may include SQL arguments. Log
		// only the concrete class so keys and payloads cannot leak.
		fields["cache_error_class"] = fmt.Sprintf("%T", event.Err)
	}
	return fields
}

func artifactCacheOutcome(kind artifact.EventKind) string {
	switch kind {
	case artifact.EventHit:
		return "hit"
	case artifact.EventMiss:
		return "miss"
	case artifact.EventComputed, artifact.EventStored:
		return "computed"
	case artifact.EventWait, artifact.EventLostRace:
		return "wait"
	case artifact.EventBypass:
		return "bypass"
	case artifact.EventCorrupt:
		return "corrupt"
	case artifact.EventStoreFailure:
		return "error_fallback"
	default:
		return "error_fallback"
	}
}
