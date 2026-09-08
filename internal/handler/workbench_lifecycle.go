package handler

func (h *WorkbenchHandler) Enabled() bool {
	return h != nil && h.service != nil && h.service.Enabled()
}
