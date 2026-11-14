package handler

// Response envelopes for Swagger annotations. Handlers write these shapes
// with gin.H; the types only exist so the documented body matches. Compose
// the payload in the annotation, e.g.
//
//	@Success 200 {object} handler.DataResponse{data=types.Knowledge}
//	@Success 200 {object} handler.PageResponse{data=[]types.Knowledge}

// DataResponse is the {"success": true, "data": ...} body.
type DataResponse struct {
	Success bool `json:"success" example:"true"`
	Data    any  `json:"data"`
}

// PageResponse is a page of a list with the total match count.
type PageResponse struct {
	Success  bool  `json:"success" example:"true"`
	Data     any   `json:"data"`
	Total    int64 `json:"total" extensions:"x-agent"`
	Page     int   `json:"page" extensions:"x-agent"`
	PageSize int   `json:"page_size" extensions:"x-agent"`
}

// DataMessageResponse is DataResponse with a human-readable message.
type DataMessageResponse struct {
	Success bool   `json:"success" example:"true"`
	Message string `json:"message"`
	Data    any    `json:"data"`
}

// MessageResponse is the {"success": true, "message": ...} body.
type MessageResponse struct {
	Success bool   `json:"success" example:"true" extensions:"x-agent"`
	Message string `json:"message"`
}
