package shutdown

import (
	"context"
	"errors"
	"os/signal"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
)

func TestShutdownFunc_run(t *testing.T) {
	t.Parallel()

	testingError := errors.New("testing")

	tests := []struct {
		name string
		fn   shutdownFunc
		want error
	}{
		{
			name: "execute function success",
			fn: shutdownFunc{
				name: "test wihtout error",
				f: func() error {
					return nil
				},
			},
			want: nil,
		},
		{
			name: "execute function return error",
			fn: shutdownFunc{
				name: "test with error",
				f: func() error {
					return testingError
				},
			},
			want: testingError,
		},
		{
			name: "execute function is nil",
			fn: shutdownFunc{
				name: "test with nil func",
				f:    nil,
			},
			want: ErrNilFunc,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			err := test.fn.run()
			assert.ErrorIs(t, err, test.want)
		})
	}
}

func TestNew(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name string
		want *ShutdownHandler
	}{
		{
			name: "success",
			want: &ShutdownHandler{},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			got := New()
			assert.Equal(t, test.want, got)
		})
	}
}

func TestShutdownHandler_Register(t *testing.T) {
	t.Parallel()

	type args struct {
		name string
		fn   func() error
	}

	testFn := func() error { return nil }

	tests := []struct {
		name        string
		handler     ShutdownHandler
		args        args
		wantHandler ShutdownHandler
	}{
		{
			name:    "add fn to handler.funcs",
			handler: ShutdownHandler{},
			args:    args{name: "test", fn: testFn},
			wantHandler: ShutdownHandler{
				funcs: []shutdownFunc{
					{name: "test", f: testFn},
				},
			},
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			test.handler.Register(test.args.name, test.args.fn)
			if assert.Equal(t, len(test.wantHandler.funcs), len(test.handler.funcs)) {
				for i := 0; i < len(test.wantHandler.funcs); i++ {
					assert.Equal(t, test.wantHandler.funcs[i].name, test.handler.funcs[i].name)
				}
			}
		})
	}
}

func TestShutdownHandler_Listen(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		shutdownFuncs []func(*testing.T, map[string]bool, *sync.WaitGroup) shutdownFunc
		timeout       time.Duration
		emitSignal    syscall.Signal
		want          error
	}{
		{
			name: "success",
			shutdownFuncs: []func(*testing.T, map[string]bool, *sync.WaitGroup) shutdownFunc{
				func(t *testing.T, m map[string]bool, wg *sync.WaitGroup) shutdownFunc {
					return shutdownFunc{
						name: "success-1",
						f: func() error {
							wg.Add(1)
							defer wg.Done()

							assert.Equal(t, 0, len(m))
							m["0"] = true

							return nil
						},
					}
				},
				func(t *testing.T, m map[string]bool, wg *sync.WaitGroup) shutdownFunc {
					return shutdownFunc{
						name: "success-2",
						f: func() error {
							wg.Add(1)
							defer wg.Done()

							assert.Equal(t, 1, len(m))
							assert.Equal(t, true, m["0"])

							return nil
						},
					}
				},
			},
			timeout:    100 * time.Millisecond,
			emitSignal: syscall.SIGINT,
			want:       nil,
		},
		{
			name: "shutdown return error",
			shutdownFuncs: []func(*testing.T, map[string]bool, *sync.WaitGroup) shutdownFunc{
				func(t *testing.T, m map[string]bool, wg *sync.WaitGroup) shutdownFunc {
					return shutdownFunc{
						name: "failing data",
						f: func() error {
							wg.Add(1)
							defer wg.Done()

							return ErrNilFunc
						},
					}
				},
			},
			timeout:    100 * time.Millisecond,
			emitSignal: syscall.SIGINT,
			want:       ErrNilFunc,
		},
		{
			name: "shutdown reach timeout",
			shutdownFuncs: []func(*testing.T, map[string]bool, *sync.WaitGroup) shutdownFunc{
				func(t *testing.T, m map[string]bool, wg *sync.WaitGroup) shutdownFunc {
					return shutdownFunc{
						name: "timeout-1",
						f: func() error {
							wg.Add(1)
							defer wg.Done()

							time.Sleep(500 * time.Millisecond)

							return nil
						},
					}
				},
				func(t *testing.T, m map[string]bool, wg *sync.WaitGroup) shutdownFunc {
					return shutdownFunc{
						name: "timeout-2",
						f: func() error {
							wg.Add(1)
							defer wg.Done()
							time.Sleep(0)

							assert.Equal(t, true, m["completed"])
							return nil
						},
					}
				},
			},
			timeout:    100 * time.Millisecond,
			emitSignal: syscall.SIGTERM,
			want:       context.DeadlineExceeded,
		},
	}

	for _, test := range tests {
		test := test
		t.Run(test.name, func(t *testing.T) {
			// t.Parallel()
			signal.Reset(test.emitSignal)

			checkMap := make(map[string]bool)
			var wg sync.WaitGroup

			handler := New(test.emitSignal)
			for _, fn := range test.shutdownFuncs {
				handler.funcs = append(handler.funcs, fn(t, checkMap, &wg))
			}

			go func() {
				time.Sleep(10 * time.Millisecond)
				syscall.Kill(syscall.Getpid(), test.emitSignal)
			}()

			got := handler.Listen(test.timeout)
			assert.ErrorIs(t, got, test.want)

			checkMap["completed"] = true
			wg.Wait()
		})
	}
}
