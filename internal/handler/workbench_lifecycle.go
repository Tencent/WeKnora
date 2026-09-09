package handler

// Enabled reports whether the optional command workbench is configured.
func (h *WorkbenchHandler) Enabled() bool {
	return h != nil && h.service != nil && h.service.Enabled()
}
