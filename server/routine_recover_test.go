package server

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

// N4: a panicking task routine (planner/worker/verifier/hypothesizer/deadline
// loop) must not kill the process — runTaskRoutine recovers, backs off, and
// re-runs fn.
func TestRunTaskRoutineRecoversFromPanic(t *testing.T) {
	old := routinePanicBackoff
	routinePanicBackoff = 10 * time.Millisecond
	t.Cleanup(func() { routinePanicBackoff = old })

	ctx, cancel := context.WithCancel(context.Background())
	rt := &taskRuntime{ctx: ctx, cancel: cancel}
	rt.wg.Add(1)

	var runs int32
	runTaskRoutine(rt, func(context.Context) {
		if atomic.AddInt32(&runs, 1) == 1 {
			panic("boom") // first run dies; the watchdog should respawn it
		}
		cancel() // rerun settles: loop returns, ctx done → goroutine exits
	})

	done := make(chan struct{})
	go func() { rt.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("routine did not settle after panic (watchdog did not respawn)")
	}
	if n := atomic.LoadInt32(&runs); n != 2 {
		t.Fatalf("runs=%d want 2 (panic → rerun)", n)
	}
}

// N4: a NORMAL return must exit for good — deadlineCoordinator returns on
// terminal status without rt.ctx being done, so a blind re-run loop would
// respawn duplicate coordinators every backoff.
func TestRunTaskRoutineExitsOnNormalReturn(t *testing.T) {
	old := routinePanicBackoff
	routinePanicBackoff = 10 * time.Millisecond
	t.Cleanup(func() { routinePanicBackoff = old })

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	rt := &taskRuntime{ctx: ctx, cancel: cancel}
	rt.wg.Add(1)

	var runs int32
	runTaskRoutine(rt, func(context.Context) {
		atomic.AddInt32(&runs, 1) // normal return (ctx still alive)
	})

	done := make(chan struct{})
	go func() { rt.wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("routine did not exit on normal return")
	}
	if n := atomic.LoadInt32(&runs); n != 1 {
		t.Fatalf("runs=%d want 1 (normal return must NOT respawn)", n)
	}
}
