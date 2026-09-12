import { createEvaluationRequestGate } from '../../api/evaluation/requestGate'
import type { DatasetImportRequest, DatasetImportResult } from '../../api/evaluation/datasets'

/** Freezes an import attempt through retries, and discards responses from an abandoned workspace. */
export function createDatasetImportSession(
  send: (request: DatasetImportRequest) => Promise<DatasetImportResult>,
  uuid: () => string = () => crypto.randomUUID(),
) {
  const gate = createEvaluationRequestGate()
  let request: DatasetImportRequest | undefined
  let inFlight: Promise<DatasetImportResult | undefined> | undefined
  let completed: DatasetImportResult | undefined
  return {
    submit(input: Omit<DatasetImportRequest, 'request_id'>): Promise<DatasetImportResult | undefined> {
      if (inFlight) return inFlight
      if (completed) return Promise.resolve(completed)
      request ??= JSON.parse(JSON.stringify({ ...input, request_id: uuid() })) as DatasetImportRequest
      const token = gate.begin(request.request_id)
      const pending = (async () => {
        try {
          const result = await send(request!)
          if (!gate.isCurrent(token)) return undefined
          completed = result
          return result
        } catch (error) {
          if (gate.isCurrent(token)) throw error
          return undefined
        } finally {
          if (gate.isCurrent(token)) inFlight = undefined
        }
      })()
      inFlight = pending
      return pending
    },
    reset() { gate.invalidate(); request = undefined; inFlight = undefined; completed = undefined },
  }
}
