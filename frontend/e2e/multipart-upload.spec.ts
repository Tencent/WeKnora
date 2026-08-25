import { expect, test } from '@playwright/test'

const ONE_MB = 1024 * 1024
const FILE_SIZE_BYTES = 100 * ONE_MB
const runs = Number.parseInt(process.env.MULTIPART_E2E_RUNS || '1', 10)

test.describe.configure({ mode: 'serial' })

test('uploads a 100MB file through Vite and retries a failed browser XHR part', async ({ context, baseURL }) => {
  expect(Number.isInteger(runs) && runs > 0).toBeTruthy()

  let failedFirstPart = false
  let failedPartNumber = ''
  const partAttempts = new Map<string, number>()
  const successfulPartResponses: Array<{ partNumber: string; etag: string }> = []

  for (let run = 1; run <= runs; run++) {
    const page = await context.newPage()
    await page.route(/\/api\/custom\/uploads\/multipart\/part(?:\?.*)?$/, async route => {
      const request = route.request()
      const headers = request.headers()
      const partNumber = headers['x-part-number'] || ''
      const attempt = headers['x-upload-attempt'] || ''
      const key = `${partNumber}:${attempt}`
      partAttempts.set(key, (partAttempts.get(key) || 0) + 1)

      if (!failedFirstPart) {
        failedFirstPart = true
        failedPartNumber = partNumber
        await route.fulfill({
          status: 503,
          contentType: 'application/json',
          body: JSON.stringify({
            code: 'e2e_proxy_interruption',
            error: 'deliberate first-part failure for retry verification',
            part_number: 1,
          }),
        })
        return
      }

      await route.continue()
    })
    page.on('response', response => {
      if (!response.url().includes('/api/custom/uploads/multipart/part') || response.status() !== 200) return
      successfulPartResponses.push({
        partNumber: response.request().headers()['x-part-number'] || '',
        etag: response.headers().etag || '',
      })
    })
    await page.route('**/api/custom/videos/**', async route => {
      if (route.request().method() !== 'GET') {
        await route.continue()
        return
      }
      await route.fulfill({
        contentType: 'application/json',
        body: JSON.stringify({
          data: {
            status: 'ready',
            thumbnail_url: '/e2e-thumbnail.jpg',
            duration_seconds: 1,
            file_url: '/e2e-video.mp4',
          },
        }),
      })
    })
    await page.addInitScript(() => {
      URL.createObjectURL = () => {
        throw new Error('multipart E2E poster generation disabled')
      }
    })
    await page.goto(baseURL || 'http://127.0.0.1:15173/', { waitUntil: 'domcontentloaded' })

    const result = await page.evaluate(async ({ fileSize, runNumber }) => {
      const { uploadVideo } = await import('/src/api/videohub/upload.ts')
      const bytes = new Uint8Array(fileSize)
      const file = new File([bytes], `multipart-e2e-${runNumber}.mp4`, { type: 'video/mp4' })
      const progress: number[] = []
      const uploaded = await uploadVideo(
        { file: { name: file.name, size: file.size, raw: file } },
        { onProgress: percent => progress.push(percent) },
        { cancelled: false },
      )
      return { id: uploaded.id, progress }
    }, { fileSize: FILE_SIZE_BYTES, runNumber: run })

    expect(result.id).not.toBe('')
    expect(result.progress.at(-1)).toBe(100)
    await page.close()
  }

  expect(failedFirstPart).toBeTruthy()
  expect(failedPartNumber).not.toBe('')
  expect(partAttempts.get(`${failedPartNumber}:1`)).toBe(runs)
  expect(partAttempts.get(`${failedPartNumber}:2`)).toBe(1)
  expect(successfulPartResponses).toHaveLength(runs * 20)
  expect(successfulPartResponses.every(response => response.partNumber !== '' && response.etag !== '')).toBeTruthy()
})
