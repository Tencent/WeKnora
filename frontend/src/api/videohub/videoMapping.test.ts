import assert from 'node:assert/strict'
import test from 'node:test'
import { formatDuration, isVideoInitiallyAvailable } from './videoMapping'

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
