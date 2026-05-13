package server

import (
	"errors"

	"github.com/jinto/kittypaw/engine"
)

func isRuntimeAdmissionBusy(err error) bool {
	return errors.Is(err, engine.ErrRuntimeAdmissionBusy)
}
