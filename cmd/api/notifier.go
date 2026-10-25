package main

import "sync"

// jobNotifier is a simple pub/sub system for handlers waiting on a job to change.
// It is NOT a representation of the job itself: it does not store any data about the job.
type jobNotifier struct {
	mu      sync.Mutex
	entries map[string]*notifierEntry
}

// notifierEntry is the per-job state for the jobNotifier. It is created when the first handler subscribes to a job, and deleted when the last handler releases it.
type notifierEntry struct {
	ch          chan struct{} // closed on the next Notify, then replaced
	subscribers int           // handlers currently registered for this job
}

// newJobNotifier creates a new jobNotifier. It is safe to call concurrently.
func newJobNotifier() *jobNotifier {
	return &jobNotifier{entries: make(map[string]*notifierEntry)}
}

// subscription represents a single handler's interest in a job. It is created by Subscribe and must be released when the handler is done waiting.
type subscription struct {
	n        *jobNotifier
	jobID    string
	released bool
}

// Subscribe registers a handler's interest in a job. It returns a subscription that must be released when the handler is done waiting.
// The subscription's Channel() method returns a channel that will be closed on the next Notify for the job, allowing the handler to wait for changes.
func (n *jobNotifier) Subscribe(jobID string) *subscription {
	n.mu.Lock()
	defer n.mu.Unlock()
	e, ok := n.entries[jobID]
	if !ok {
		e = &notifierEntry{ch: make(chan struct{})}
		n.entries[jobID] = e
	}
	e.subscribers++
	return &subscription{n: n, jobID: jobID}
}

// Channel returns a channel that will be closed on the next Notify for the job. It is safe to call multiple times, but the channel will always be the same until Notify is called, at which point it will be replaced with a new channel.
func (s *subscription) Channel() <-chan struct{} {
	s.n.mu.Lock()
	defer s.n.mu.Unlock()
	return s.n.entries[s.jobID].ch
}

// Release releases the subscription, decrementing the subscriber count for the job. If this was the last subscriber, the entry for the job is deleted from the notifier. It is safe to call multiple times, but only the first call has an effect.
func (s *subscription) Release() {
	if s.released {
		return
	}
	s.released = true
	s.n.mu.Lock()
	defer s.n.mu.Unlock()
	e, ok := s.n.entries[s.jobID]
	if !ok {
		return
	}
	e.subscribers--
	if e.subscribers <= 0 {
		delete(s.n.entries, s.jobID) // nobody is waiting: free the bookkeeping
	}
}

// Notify wakes up all handlers waiting on the job. It must only be called AFTER the
// database transaction that changed the job has committed. A signal is
// never a representation: woken handlers always re-read PostgreSQL.
func (n *jobNotifier) Notify(jobID string) {
	n.mu.Lock()
	defer n.mu.Unlock()
	e, ok := n.entries[jobID]
	if !ok {
		return // no one waiting; later arrivals will read the committed row anyway
	}
	close(e.ch)
	e.ch = make(chan struct{})
}

// ActiveJobs returns the number of jobs that currently have at least one subscriber.
// It is used for monitoring and debugging, and is safe to call concurrently.
func (n *jobNotifier) ActiveJobs() int {
	n.mu.Lock()
	defer n.mu.Unlock()
	return len(n.entries)
}
