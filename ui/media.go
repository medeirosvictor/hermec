package ui

import "sync"

// mediaWorker runs the client's media calls (StartShare, StopShare, Watch) one
// at a time, in the order they were queued, off the game goroutine: they can
// wait on the encoder or the network. Results go back to the game goroutine as
// closures that pump applies during Update.
type mediaWorker struct {
	mu      sync.Mutex
	queue   []func()
	results []func()
	pending int // queued or running ops
	wake    chan struct{}
	started bool
}

// do queues f behind every earlier op.
func (m *mediaWorker) do(f func()) {
	m.mu.Lock()
	if !m.started {
		m.started = true
		m.wake = make(chan struct{}, 1)
		go m.loop()
	}
	m.queue = append(m.queue, f)
	m.pending++
	m.mu.Unlock()
	select {
	case m.wake <- struct{}{}:
	default:
	}
}

// post hands a result to the game goroutine (worker side).
func (m *mediaWorker) post(f func()) {
	m.mu.Lock()
	m.results = append(m.results, f)
	m.mu.Unlock()
}

func (m *mediaWorker) loop() {
	for range m.wake {
		for {
			m.mu.Lock()
			if len(m.queue) == 0 {
				m.mu.Unlock()
				break
			}
			f := m.queue[0]
			m.queue = m.queue[1:]
			m.mu.Unlock()
			f()
			m.mu.Lock()
			m.pending--
			m.mu.Unlock()
		}
	}
}

// pump runs the posted results (game goroutine).
func (m *mediaWorker) pump() {
	m.mu.Lock()
	rs := m.results
	m.results = nil
	m.mu.Unlock()
	for _, f := range rs {
		f()
	}
}

// idle reports no op queued or running.
func (m *mediaWorker) idle() bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.pending == 0
}
