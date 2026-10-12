//go:build !unix

package skilltree

// Lock is process-local elsewhere: only macOS ships a host sandbox today.
func (t *Tree) Lock(name string) (func(), error) {
	if err := validName(name); err != nil {
		return nil, err
	}
	return func() {}, nil
}

func (t *Tree) tryLock(name string) (func(), bool, error) {
	unlock, err := t.Lock(name)
	return unlock, err == nil, err
}
