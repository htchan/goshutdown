package shutdown

import (
	"context"
	"errors"
	"flag"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"go.uber.org/goleak"
)

func TestMain(m *testing.M) {
	leak := flag.Bool("leak", false, "check for goroutine leaks")
	flag.Parse()

	if *leak {
		goleak.VerifyTestMain(m)
	} else {
		os.Exit(m.Run())
	}
}

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

// syncMapLen returns the number of entries in a sync.Map.
func syncMapLen(m *sync.Map) int {
	count := 0
	m.Range(func(_, _ any) bool {
		count++
		return true
	})
	return count
}

func TestShutdownHandler_Listen(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name          string
		shutdownFuncs []func(*testing.T, *sync.Map, *sync.WaitGroup) shutdownFunc
		timeout       time.Duration
		emitSignal    syscall.Signal
		want          error
	}{
		{
			name: "success",
			shutdownFuncs: []func(*testing.T, *sync.Map, *sync.WaitGroup) shutdownFunc{
				func(t *testing.T, m *sync.Map, wg *sync.WaitGroup) shutdownFunc {
					return shutdownFunc{
						name: "success-1",
						f: func() error {
							wg.Add(1)
							defer wg.Done()

							assert.Equal(t, 0, syncMapLen(m))
							m.Store("0", true)

							return nil
						},
					}
				},
				func(t *testing.T, m *sync.Map, wg *sync.WaitGroup) shutdownFunc {
					return shutdownFunc{
						name: "success-2",
						f: func() error {
							wg.Add(1)
							defer wg.Done()

							assert.Equal(t, 1, syncMapLen(m))
							v, ok := m.Load("0")
							assert.True(t, ok)
							assert.Equal(t, true, v)

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
			shutdownFuncs: []func(*testing.T, *sync.Map, *sync.WaitGroup) shutdownFunc{
				func(t *testing.T, m *sync.Map, wg *sync.WaitGroup) shutdownFunc {
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
			name: "shutdown function panics",
			shutdownFuncs: []func(*testing.T, *sync.Map, *sync.WaitGroup) shutdownFunc{
				func(t *testing.T, m *sync.Map, wg *sync.WaitGroup) shutdownFunc {
					return shutdownFunc{
						name: "panicking",
						f: func() error {
							panic("something went wrong")
						},
					}
				},
				func(t *testing.T, m *sync.Map, wg *sync.WaitGroup) shutdownFunc {
					return shutdownFunc{
						name: "after-panic",
						f: func() error {
							wg.Add(1)
							defer wg.Done()

							m.Store("after-panic", true)
							return nil
						},
					}
				},
			},
			timeout:    100 * time.Millisecond,
			emitSignal: syscall.SIGINT,
			want:       ErrPanic,
		},
		{
			name: "shutdown reach timeout",
			shutdownFuncs: []func(*testing.T, *sync.Map, *sync.WaitGroup) shutdownFunc{
				func(t *testing.T, m *sync.Map, wg *sync.WaitGroup) shutdownFunc {
					wg.Add(1)
					return shutdownFunc{
						name: "timeout-1",
						f: func() error {
							defer wg.Done()

							time.Sleep(500 * time.Millisecond)

							return nil
						},
					}
				},
				func(t *testing.T, m *sync.Map, wg *sync.WaitGroup) shutdownFunc {
					// timeout-2 never runs because timeout-1 exceeds the deadline,
					// so we don't add to wg here.
					return shutdownFunc{
						name: "timeout-2",
						f: func() error {
							wg.Add(1)
							defer wg.Done()

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
		t.Run(test.name, func(t *testing.T) {
			// t.Parallel()
			signal.Reset(test.emitSignal)

			checkMap := &sync.Map{}
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

			checkMap.Store("completed", true)
			wg.Wait()
		})
	}
}
