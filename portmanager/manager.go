package portmanager

import (
	"fmt"
	"net"
	"sync"
)

const DefaultHost = "127.0.0.1"

type Range struct {
	Start int
	End   int
}

type Checker interface {
	Available(port int) bool
}

type TCPChecker struct {
	Host string
}

func (c TCPChecker) Available(port int) bool {
	host := c.Host
	if host == "" {
		host = DefaultHost
	}

	listener, err := net.Listen("tcp", fmt.Sprintf("%s:%d", host, port))
	if err != nil {
		return false
	}
	_ = listener.Close()
	return true
}

type Manager struct {
	mu           sync.Mutex
	checker      Checker
	reservations map[string]int
	reservedBy   map[int]string
	lastAssigned map[string]int
}

func NewManager(checker Checker) *Manager {
	if checker == nil {
		checker = TCPChecker{Host: DefaultHost}
	}

	return &Manager{
		checker:      checker,
		reservations: make(map[string]int),
		reservedBy:   make(map[int]string),
		lastAssigned: make(map[string]int),
	}
}

func (m *Manager) Assign(taskID string, preferred int, portRange Range) (int, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	if taskID == "" {
		return 0, fmt.Errorf("task id is required")
	}

	for _, port := range candidates(m.lastAssigned[taskID], preferred, portRange) {
		if owner, reserved := m.reservedBy[port]; reserved {
			if owner == taskID {
				m.lastAssigned[taskID] = port
				return port, nil
			}
			continue
		}

		if !m.checker.Available(port) {
			continue
		}

		m.replaceReservation(taskID, port)
		m.lastAssigned[taskID] = port
		return port, nil
	}

	return 0, fmt.Errorf("no available port for task %s in range %d-%d", taskID, portRange.Start, portRange.End)
}

func (m *Manager) Release(taskID string) {
	m.mu.Lock()
	defer m.mu.Unlock()

	port, exists := m.reservations[taskID]
	if !exists {
		return
	}

	delete(m.reservations, taskID)
	delete(m.reservedBy, port)
}

func (m *Manager) LastAssigned(taskID string) (int, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()

	port, exists := m.lastAssigned[taskID]
	return port, exists
}

func (m *Manager) replaceReservation(taskID string, port int) {
	if currentPort, exists := m.reservations[taskID]; exists {
		delete(m.reservedBy, currentPort)
	}

	m.reservations[taskID] = port
	m.reservedBy[port] = taskID
}

func candidates(lastAssigned int, preferred int, portRange Range) []int {
	seen := make(map[int]bool)
	result := make([]int, 0)

	add := func(port int) {
		if port == 0 || seen[port] {
			return
		}
		seen[port] = true
		result = append(result, port)
	}

	add(lastAssigned)
	add(preferred)

	if portRange.Start > 0 && portRange.End >= portRange.Start {
		for port := portRange.Start; port <= portRange.End; port++ {
			add(port)
		}
	}

	return result
}
