package service

// HTTPError is a transport-agnostic error that can be mapped to an HTTP response.
// transport layer should convert it to its framework-specific error type.
type HTTPError struct {
	Status  int
	Message string
}

func (e *HTTPError) Error() string {
	if e == nil {
		return ""
	}
	return e.Message
}

func NewHTTPError(status int, message string) *HTTPError {
	return &HTTPError{Status: status, Message: message}
}
