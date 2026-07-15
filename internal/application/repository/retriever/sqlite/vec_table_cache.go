package sqlite

// validateCachedVecTable confirms that a cached vec table still exists in the
// current database view. Transactional DDL may roll back after the cache was
// populated, in which case the entry is evicted so the caller can recreate it.
func validateCachedVecTable(
	cache map[int]bool,
	dimension int,
	exists func() (bool, error),
) (bool, error) {
	if !cache[dimension] {
		return false, nil
	}
	present, err := exists()
	if err != nil {
		return false, err
	}
	if !present {
		delete(cache, dimension)
		return false, nil
	}
	return true, nil
}
