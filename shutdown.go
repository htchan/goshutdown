package shutdown

import (
	"context"
	"errors"
	"os"
	"os/signal"
	"time"

	"golang.org/x/exp/slog"
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
		f := func() <-chan struct{} {
			fnComplete := make(chan struct{}, 1)

			go func() {
				defer close(fnComplete)

				if err := fn.run(); err != nil {
					errGroup = errors.Join(errGroup, err)
					if LogEnabled {
						slog.Error("shutdown error", "name", fn.name, "error", err)
					}
				} else if LogEnabled {
					slog.Info("shutdown completed", "name", fn.name)
				}

				fnComplete <- struct{}{}
			}()

			return fnComplete
		}

		select {
		case <-f():
		case <-ctx.Done():
			if LogEnabled {
				slog.Info("shutdown timeout", "name", fn.name)
			}
		}
	}

	errGroup = errors.Join(errGroup, ctx.Err())

	return errGroup
}
