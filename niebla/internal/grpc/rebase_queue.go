package grpc

import (
	"context"
	"sync"
	"sync/atomic"
)

// RebaseTask represents a pending background cascading rebase request.
type RebaseTask struct {
	ParentCLID    int64
	OldParentTree []byte
	NewParentTree []byte
	Done          chan struct{}
}

// KeyedMutex provides per-ID mutual exclusion so operations on the same changelist
// branch are strictly serialized while disjoint branches proceed in parallel.
type KeyedMutex struct {
	mu    sync.Mutex
	locks map[int64]*sync.Mutex
}

// NewKeyedMutex initializes a new KeyedMutex.
func NewKeyedMutex() *KeyedMutex {
	return &KeyedMutex{
		locks: make(map[int64]*sync.Mutex),
	}
}

// Lock acquires the mutex associated with id and returns an unlock function.
func (km *KeyedMutex) Lock(id int64) func() {
	km.mu.Lock()
	if km.locks == nil {
		km.locks = make(map[int64]*sync.Mutex)
	}
	m, ok := km.locks[id]
	if !ok {
		m = &sync.Mutex{}
		km.locks[id] = m
	}
	km.mu.Unlock()

	m.Lock()
	return func() {
		m.Unlock()
	}
}

// RebaseExecutor is the callback executed for each cascading rebase task.
type RebaseExecutor func(ctx context.Context, parentCLID int64, oldParentTree, newParentTree []byte) ([]int64, error)

// RebaseQueue manages asynchronous execution of stack rebase jobs across background workers.
type RebaseQueue struct {
	tasks    chan RebaseTask
	executor RebaseExecutor
	locks    *KeyedMutex
	ctx      context.Context
	cancel   context.CancelFunc
	wg       sync.WaitGroup
	inFlight int64
	closed   atomic.Bool
	idleCond *sync.Cond
	idleMu   sync.Mutex
}

// NewRebaseQueue initializes and starts background workers for rebase task processing.
func NewRebaseQueue(numWorkers int, bufferSize int, executor RebaseExecutor) *RebaseQueue {
	if numWorkers <= 0 {
		numWorkers = 2
	}
	if bufferSize <= 0 {
		bufferSize = 256
	}

	ctx, cancel := context.WithCancel(context.Background())
	queue := &RebaseQueue{
		tasks:    make(chan RebaseTask, bufferSize),
		executor: executor,
		locks:    NewKeyedMutex(),
		ctx:      ctx,
		cancel:   cancel,
	}
	queue.idleCond = sync.NewCond(&queue.idleMu)

	for i := 0; i < numWorkers; i++ {
		queue.wg.Add(1)
		go queue.workerLoop()
	}

	return queue
}

func (q *RebaseQueue) workerLoop() {
	defer q.wg.Done()

	for {
		select {
		case <-q.ctx.Done():
			return
		case task, ok := <-q.tasks:
			if !ok {
				return
			}
			q.processTask(task)
		}
	}
}

func (q *RebaseQueue) processTask(task RebaseTask) {
	defer func() {
		if task.Done != nil {
			close(task.Done)
		}
		atomic.AddInt64(&q.inFlight, -1)
		q.idleMu.Lock()
		q.idleCond.Broadcast()
		q.idleMu.Unlock()
	}()

	if q.executor == nil {
		return
	}

	unlock := q.locks.Lock(task.ParentCLID)
	defer unlock()

	_, _ = q.executor(q.ctx, task.ParentCLID, task.OldParentTree, task.NewParentTree)
}

// Enqueue submits a rebase task to the background queue.
// Returns a completion channel that will be closed when the task finishes processing.
func (q *RebaseQueue) Enqueue(parentCLID int64, oldParentTree, newParentTree []byte) <-chan struct{} {
	done := make(chan struct{})
	if q.closed.Load() || parentCLID <= 0 {
		close(done)
		return done
	}

	task := RebaseTask{
		ParentCLID:    parentCLID,
		OldParentTree: oldParentTree,
		NewParentTree: newParentTree,
		Done:          done,
	}

	atomic.AddInt64(&q.inFlight, 1)

	select {
	case q.tasks <- task:
		return done
	default:
		// Queue buffer is full; run asynchronously to avoid blocking caller
		go q.processTask(task)
		return done
	}
}

// InFlight returns the number of tasks currently queued or actively executing.
func (q *RebaseQueue) InFlight() int64 {
	return atomic.LoadInt64(&q.inFlight)
}

// WaitIdle blocks until all pending and currently running rebase tasks have finished.
func (q *RebaseQueue) WaitIdle() {
	q.idleMu.Lock()
	defer q.idleMu.Unlock()
	for atomic.LoadInt64(&q.inFlight) > 0 {
		q.idleCond.Wait()
	}
}

// Stop gracefully shuts down the rebase worker queue and waits for all active tasks to complete.
func (q *RebaseQueue) Stop() {
	if q.closed.CompareAndSwap(false, true) {
		q.cancel()
		close(q.tasks)
		q.wg.Wait()
	}
}
