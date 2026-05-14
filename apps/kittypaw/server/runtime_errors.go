package server

import (
	"errors"
	"net/http"

	"github.com/jinto/kittypaw/engine"
)

func isRuntimeAdmissionBusy(err error) bool {
	return errors.Is(err, engine.ErrRuntimeAdmissionBusy)
}

func isLLMRateLimitExceeded(err error) bool {
	return errors.Is(err, engine.ErrLLMRateLimitExceeded)
}

func runtimeErrorHTTPStatus(err error) (int, string, bool) {
	switch {
	case isRuntimeAdmissionBusy(err):
		return http.StatusTooManyRequests, "runtime busy", true
	case isLLMRateLimitExceeded(err):
		return http.StatusTooManyRequests, err.Error(), true
	case errors.Is(err, engine.ErrDailyTokenLimitExceeded):
		return http.StatusTooManyRequests, err.Error(), true
	default:
		return http.StatusInternalServerError, "", false
	}
}

func runtimeErrorMessage(err error) (string, bool) {
	switch {
	case isRuntimeAdmissionBusy(err):
		return "runtime busy", true
	case isLLMRateLimitExceeded(err):
		return err.Error(), true
	case errors.Is(err, engine.ErrDailyTokenLimitExceeded):
		return err.Error(), true
	default:
		return "", false
	}
}
