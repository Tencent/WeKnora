package types

// ContextKey defines a type for context keys to avoid string collision
type ContextKey string

const (
	// TenantIDContextKey is the context key for tenant ID
	TenantIDContextKey ContextKey = "TenantID"
	// TenantInfoContextKey is the context key for tenant information
	TenantInfoContextKey ContextKey = "TenantInfo"
	// RequestIDContextKey is the context key for request ID
	RequestIDContextKey ContextKey = "RequestID"
	// LoggerContextKey is the context key for logger
	LoggerContextKey ContextKey = "Logger"
	// UserContextKey is the context key for user information
	UserContextKey ContextKey = "User"
)

// String returns the string representation of the context key
func (c ContextKey) String() string {
	return string(c)
}
