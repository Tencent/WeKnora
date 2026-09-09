import assert from 'node:assert/strict'
import test from 'node:test'
import { formatDuration, isVideoInitiallyAvailable, mapVideo } from './videoMapping'

test('maps salon-compatible video types to the meeting label', () => {
	assert.equal(mapVideo({ video_type: 'salon', video_type_generated: true }).categoryName, '会议')
	assert.equal(mapVideo({ video_type: 'lecture', video_type_generated: true }).categoryName, '会议')
	assert.equal(mapVideo({ video_type: 'meeting', video_type_generated: true }).categoryName, '会议')
	assert.equal(mapVideo({ video_type: 'meeting', video_type_generated: true }).category, 'meeting')
})

test('hides the upload default category until the summary model generates a type', () => {
	const pending = mapVideo({ video_type: 'training' })
	assert.equal(pending.categoryName, '')
	assert.equal(pending.videoTypeGenerated, false)

	const generated = mapVideo({ video_type: 'training', video_type_generated: true })
	assert.equal(generated.categoryName, '培训')
	assert.equal(generated.videoTypeGenerated, true)
})

test('formats video durations as HH:MM:SS', () => {
	assert.equal(formatDuration(0), '—')
	assert.equal(formatDuration(65), '00:01:05')
	assert.equal(formatDuration(3661), '01:01:01')
})

test('keeps an uploaded video playable after content parsing fails', () => {
	assert.equal(isVideoInitiallyAvailable({ status: 'failed', file_url: 'https://cdn.example.com/video.mp4' }), true)
	assert.equal(isVideoInitiallyAvailable({ status: 'failed', file_url: '' }), false)
})

test('respects an explicit unavailable response for an incomplete upload', () => {
	assert.equal(isVideoInitiallyAvailable({
		status: 'failed',
		file_url: 'https://cdn.example.com/video.mp4',
		initially_available: false,
	}), false)
})
