package envai

import (
	"errors"
	"net/http"
)

type rejectedAdmissionError struct {
	err error
}

func (e *rejectedAdmissionError) Error() string {
	return e.err.Error()
}
func (e *rejectedAdmissionError) Unwrap() error {
	return e.err
}
func rejectedAdmission(err error) error {
	return &rejectedAdmissionError{err: err}
}
func AdmissionRejected(err error) bool {
	var rejected *rejectedAdmissionError
	if errors.As(err, &rejected) {
		return true
	}
	var response *APIError
	if errors.As(err, &response) {
		switch response.Status {
		case http.StatusBadRequest, http.StatusUnauthorized, http.StatusPaymentRequired, http.StatusForbidden, http.StatusNotFound, http.StatusMethodNotAllowed, http.StatusGone, http.StatusRequestEntityTooLarge, http.StatusUnsupportedMediaType, http.StatusUnprocessableEntity, http.StatusTooManyRequests:
			return true
		}
	}
	return false
}
