// http_error.go defines the service-layer error that transport converts into framework errors.
package service

// HTTPError is a transport-agnostic error that can be mapped to an HTTP response.
// transport layer should convert it to its framework-specific error type.
type HTTPError struct {
	Status  int
	Message string
}

// Error returns the user-facing message carried by the service error.
func (e *HTTPError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

// NewHTTPError creates a service error with a transport-oriented status code and message.
func NewHTTPError(status int, message string) *HTTPError {
	return &HTTPError{Status: status, Message: message}
}
