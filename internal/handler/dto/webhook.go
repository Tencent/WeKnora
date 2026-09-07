package dto

type WebhookEndpointCreateRequest struct {
	Name        string   `json:"name"`
	URL         string   `json:"url"`
	Secret      string   `json:"secret"`
	Events      []string `json:"events"`
	Enabled     *bool    `json:"enabled"`
	Description string   `json:"description"`
}

type WebhookEndpointUpdateRequest struct {
	Name        *string  `json:"name"`
	URL         *string  `json:"url"`
	Secret      *string  `json:"secret"`
	Events      []string `json:"events"`
	Enabled     *bool    `json:"enabled"`
	Description *string  `json:"description"`
}

type WebhookRenewResponse struct {
	Ticket           string `json:"ticket"`
	TicketExpiresAt  string `json:"ticket_expires_at"`
	Path             string `json:"path"`
	TicketHeader     string `json:"ticket_header"`
}
