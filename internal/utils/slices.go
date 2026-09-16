package utils

// ChunkSlice splits a slice into multiple sub-slices of the specified size
func ChunkSlice[T any](slice []T, chunkSize int) [][]T {
	// Handle edge cases
	if len(slice) == 0 {
		return [][]T{}
	}

	if chunkSize <= 0 {
		panic("chunkSize must be greater than 0")
	}

	// Calculate how many sub-slices are needed
	chunks := make([][]T, 0, (len(slice)+chunkSize-1)/chunkSize)

	// Split the slice
	for i := 0; i < len(slice); i += chunkSize {
		end := i + chunkSize
		if end > len(slice) {
			end = len(slice)
		}
		chunks = append(chunks, slice[i:end])
	}

	return chunks
}

// MapSlice applies a function to each element of a slice and returns a new slice with the results
func MapSlice[A any, B any](in []A, f func(A) B) []B {
	out := make([]B, 0, len(in))
	for _, item := range in {
		out = append(out, f(item))
	}
	return out
}
