//go:build !unix

package skilltree

// Lock is process-local elsewhere: only macOS ships a host sandbox today.
func (t *Tree) Lock(name string) (func(), error) {
	if err := validName(name); err != nil {
		return nil, err
	}
	return func() {}, nil
}
