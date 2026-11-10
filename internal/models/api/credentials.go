package api

// Credentials is what the operator stored for one model row.
type Credentials struct {
	APIKey    string
	AppID     string
	AppSecret string
}

// Ptr returns a pointer for optional protocol settings.
func Ptr[T any](value T) *T { return &value }
