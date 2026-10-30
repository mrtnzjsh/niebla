package grpc

import (
	"bytes"
	"context"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestRebaseQueue_AsyncExecution(t *testing.T) {
	var called atomic.Bool
	var executedParent int64
	var executedOld, executedNew []byte

	executor := func(ctx context.Context, parentCLID int64, oldParentTree, newParentTree []byte) ([]int64, error) {
		called.Store(true)
		executedParent = parentCLID
		executedOld = oldParentTree
		executedNew = newParentTree
		return []int64{101}, nil
	}

	queue := NewRebaseQueue(2, 10, executor)
	defer queue.Stop()

	oldTree := []byte("old-tree-hash")
	newTree := []byte("new-tree-hash")

	done := queue.Enqueue(42, oldTree, newTree)

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for rebase task completion")
	}

	if !called.Load() {
		t.Errorf("expected executor to be called")
	}
	if executedParent != 42 {
		t.Errorf("expected parent CL 42, got %d", executedParent)
	}
	if !bytes.Equal(executedOld, oldTree) || !bytes.Equal(executedNew, newTree) {
		t.Errorf("mismatched trees passed to executor")
	}
}

func TestRebaseQueue_PerCLLocking(t *testing.T) {
	// Tasks for the same CL should not run concurrently
	var sameCLConcurrent int64
	var maxSameCLConcurrent int64

	executor := func(ctx context.Context, parentCLID int64, oldParentTree, newParentTree []byte) ([]int64, error) {
		if parentCLID == 100 {
			current := atomic.AddInt64(&sameCLConcurrent, 1)
			defer atomic.AddInt64(&sameCLConcurrent, -1)

			for {
				max := atomic.LoadInt64(&maxSameCLConcurrent)
				if current <= max || atomic.CompareAndSwapInt64(&maxSameCLConcurrent, max, current) {
					break
				}
			}
			time.Sleep(50 * time.Millisecond)
		}
		return nil, nil
	}

	queue := NewRebaseQueue(4, 10, executor)
	defer queue.Stop()

	var wg sync.WaitGroup
	for i := 0; i < 5; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			<-queue.Enqueue(100, []byte("old"), []byte("new"))
		}()
	}

	wg.Wait()

	if maxSameCLConcurrent > 1 {
		t.Errorf("expected serialized execution on same CL (max concurrent 1), got %d", maxSameCLConcurrent)
	}
}

func TestRebaseQueue_WaitIdleAndStop(t *testing.T) {
	var count int64
	executor := func(ctx context.Context, parentCLID int64, oldParentTree, newParentTree []byte) ([]int64, error) {
		time.Sleep(20 * time.Millisecond)
		atomic.AddInt64(&count, 1)
		return nil, nil
	}

	queue := NewRebaseQueue(2, 50, executor)

	for i := int64(1); i <= 10; i++ {
		queue.Enqueue(i, []byte("old"), []byte("new"))
	}

	queue.WaitIdle()

	if atomic.LoadInt64(&count) != 10 {
		t.Errorf("expected 10 tasks completed at WaitIdle, got %d", count)
	}
	if queue.InFlight() != 0 {
		t.Errorf("expected 0 in-flight tasks, got %d", queue.InFlight())
	}

	queue.Stop()
}
