# Opt-in background embedding token budget

Set `WEKNORA_EMBEDDING_TPM=60000` to pace remote background embeddings. Unset, invalid or non-positive values leave the budget disabled and preserve existing retry behavior. Root Compose forwards the setting; recreate the app to change it.

## Scope

The budget registry is in one app process. Calls with the same endpoint (trailing slash removed) and model share one budget, including duplicate database model records, different credentials, and different documents. Separate processes/replicas do **not** share this state. Local embeddings and interactive query embeddings bypass this budget. It is not an account-wide or cluster-wide provider quota guarantee.

Estimated request cost is `16 + sum(2 * rune_count(text) + 8)`. This conservative estimate is not the provider tokenizer and does not validate maximum input length. Keep chunks within provider limits and use a conservative `BATCH_EMBED_SIZE`.

## Scheduling and cooldown

One slot serializes requests for each endpoint/model. The slot stays held during provider calls, pacing waits, and retries. This deliberately prevents competing documents from spending separate minute allowances, but also limits throughput to at most one in-flight request for that scope, even when a higher concurrency limit is configured.

A request reserves spacing proportional to its estimated cost and the working TPM. A quota rejection (`429`, `insufficient_quota`, or `throttling`) applies a one-minute shared cooldown and halves the working TPM while it is above 5000, with a floor of 5000. Configured limits below 5000 are never increased. The reduction **does not recover automatically** after successful calls; restarting the process resets it to the environment setting. Provider `Retry-After` is not interpreted. Cancellation interrupts waiting and releases the slot.

## Exhaustion

The failed sub-batch is tried up to five times (initial call plus four retries). Other errors return immediately. Exhaustion returns `ErrBudgetRetriesExhausted` joined with the provider error. `batchEmbedWithBackoff` then stops its outer whole-batch retry so it does not multiply those five quota attempts. When the budget is disabled, ordinary quota errors retain the previous outer backoff behavior.

This PR adds no persistent result cache. Successful work is not guaranteed to survive a new document retry or process restart. It does not change the batch worker race fix, MinerU parsing, or storage behavior.
