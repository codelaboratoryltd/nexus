package state

import (
	"context"
	"errors"

	ds "github.com/ipfs/go-datastore"

	"github.com/codelaboratoryltd/nexus/internal/keys"
	"github.com/codelaboratoryltd/nexus/internal/store"
)

// WorkItem represents a CRDT update to be processed.
type WorkItem struct {
	Key   ds.Key
	Value []byte // nil for deletions
}

// startWorkerPool starts worker goroutines to process CRDT updates.
func (s *State) startWorkerPool(ctx context.Context, numWorkers int) {
	for i := 0; i < numWorkers; i++ {
		go s.worker(ctx, i)
	}
}

// worker processes work items from the queue.
func (s *State) worker(ctx context.Context, id int) {
	s.log.Infof("Worker %d started", id)
	for {
		select {
		case work := <-s.workQueue:
			s.workerProcess(ctx, work, id)
		case <-ctx.Done():
			s.log.Infof("Worker %d stopping", id)
			return
		}
	}
}

// workerProcess routes work items to appropriate handlers.
func (s *State) workerProcess(ctx context.Context, work WorkItem, id int) {
	if work.Key.IsDescendantOf(keys.AllocationKey) {
		s.processAllocationWork(ctx, work, id)
		return
	}
	if work.Key.IsDescendantOf(keys.PoolKey) {
		s.processPoolWork(ctx, work, id)
		return
	}
}

// processAllocationWork handles allocation/deallocation updates.
func (s *State) processAllocationWork(ctx context.Context, work WorkItem, id int) {
	if work.Value == nil {
		err := s.handleDeallocation(ctx, work.Key)
		if err != nil && !errors.Is(err, store.ErrMalformedKey) {
			s.log.Errorf("Worker %d handle deallocation error: %s", id, err)
		}
	} else {
		err := s.handleAllocation(ctx, work.Key, work.Value)
		if err != nil && !errors.Is(err, store.ErrMalformedKey) {
			s.log.Errorf("Worker %d handle allocation error: %s", id, err)
		}
	}
}

// processPoolWork handles pool create/update/delete.
func (s *State) processPoolWork(ctx context.Context, work WorkItem, id int) {
	if work.Value == nil {
		err := s.handlePoolDelete(ctx, work.Key)
		if err != nil {
			s.log.Errorf("Worker %d handle pool delete error: %s", id, err)
		}
	} else {
		err := s.handlePoolUpdate(ctx, work.Key, work.Value)
		if err != nil {
			s.log.Errorf("Worker %d handle pool update error: %s", id, err)
		}
	}
}

// enqueueWork adds a work item to the processing queue.
func (s *State) enqueueWork(key ds.Key, value []byte) {
	select {
	case s.workQueue <- WorkItem{Key: key, Value: value}:
		s.log.Debugf("Enqueued work for key: %s", key)
	default:
		s.log.Errorf("Work queue is full! Dropping work for key: %s", key)
	}
}
