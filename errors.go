package shutdown

import "errors"

var (
	ErrNilFunc = errors.New("nil function")
	ErrPanic   = errors.New("panic during shutdown")
)
