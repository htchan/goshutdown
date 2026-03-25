package shutdown

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"time"

	"log/slog"
)

var LogEnabled = false

// TODO: move this to separate package
type shutdownFunc struct {
	name string
	f    func() error
}

func (f *shutdownFunc) run() error {
	if f.f == nil {
		return ErrNilFunc
	}

	return f.f()
}

type ShutdownHandler struct {
	funcs   []shutdownFunc
	signals []os.Signal
}

func New(signals ...os.Signal) *ShutdownHandler {
	return &ShutdownHandler{
		signals: signals,
	}
}

func (handler *ShutdownHandler) Register(name string, f func() error) {
	handler.funcs = append(handler.funcs, shutdownFunc{name: name, f: f})
}

func (handler *ShutdownHandler) Listen(timeout time.Duration) error {
	kill := make(chan os.Signal, 1)
	signal.Notify(kill, handler.signals...)
	<-kill

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	var errGroup error

	// run ShutdownFunc one by one
	for _, fn := range handler.funcs {
		f := func() <-chan error {
			errCh := make(chan error, 1)

			go func() {
				defer close(errCh)

				defer func() {
					if r := recover(); r != nil {
						if LogEnabled {
							slog.Error("shutdown panic", "name", fn.name, "panic", r)
						}
						errCh <- fmt.Errorf("%w: %v", ErrPanic, r)
						return
					}
				}()

				err := fn.run()
				if err != nil && LogEnabled {
					slog.Error("shutdown error", "name", fn.name, "error", err)
				} else if LogEnabled {
					slog.Info("shutdown completed", "name", fn.name)
				}

				errCh <- err
			}()

			return errCh
		}

		select {
		case err := <-f():
			errGroup = errors.Join(errGroup, err)
		case <-ctx.Done():
			if LogEnabled {
				slog.Info("shutdown timeout", "name", fn.name)
			}
		}
	}

	errGroup = errors.Join(errGroup, ctx.Err())

	return errGroup
}
