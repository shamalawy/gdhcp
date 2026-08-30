package ha

import (
	"context"
	"fmt"
	"sync"

	"github.com/shamalawy/gdhcp/server/internal/models"
)

// mockStore is a minimal, in-memory implementation of dataStore for tests.
// It deliberately does not implement store.Store — the whole point of
// dataStore (cluster.go) is that this package's tests never need to stub
// the other ~70 methods on the full interface.
type mockStore struct {
	mu sync.Mutex

	cluster *models.Cluster
	servers []*models.ClusterServer

	heartbeats map[string]int // serverID -> write count

	electionResult string
	electionErr    error

	heartbeatErr error

	registerErr error
	// registerAssignsID controls whether RegisterServer mutates the passed
	// struct's ID (true, the common repository convention) or leaves it
	// empty so ensureServerRegistered must fall back to ListServers.
	registerAssignsID bool

	health    *models.ClusterHealth
	healthErr error

	listenErr error
}

func newMockStore() *mockStore {
	return &mockStore{
		heartbeats:        map[string]int{},
		registerAssignsID: true,
	}
}

func (s *mockStore) GetClusterByID(ctx context.Context, clusterID string) (*models.Cluster, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.cluster == nil || s.cluster.ID != clusterID {
		return nil, models.ErrNotFound
	}
	return s.cluster, nil
}

func (s *mockStore) CreateCluster(ctx context.Context, c *models.Cluster) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.cluster = c
	return nil
}

func (s *mockStore) RegisterServer(ctx context.Context, server *models.ClusterServer) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.registerErr != nil {
		return s.registerErr
	}
	for _, existing := range s.servers {
		if existing.NodeID == server.NodeID {
			if s.registerAssignsID {
				server.ID = existing.ID
			}
			return nil
		}
	}
	id := fmt.Sprintf("server-%d", len(s.servers)+1)
	stored := *server
	stored.ID = id
	s.servers = append(s.servers, &stored)
	if s.registerAssignsID {
		server.ID = id
	}
	return nil
}

func (s *mockStore) ListServers(ctx context.Context, clusterID string) ([]*models.ClusterServer, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]*models.ClusterServer, len(s.servers))
	copy(out, s.servers)
	return out, nil
}

func (s *mockStore) WriteHeartbeat(ctx context.Context, clusterID, serverID string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.heartbeatErr != nil {
		return s.heartbeatErr
	}
	s.heartbeats[serverID]++
	return nil
}

func (s *mockStore) ElectionQuery(ctx context.Context, clusterID string) (string, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.electionErr != nil {
		return "", s.electionErr
	}
	return s.electionResult, nil
}

func (s *mockStore) GetClusterHealth(ctx context.Context, clusterID string) (*models.ClusterHealth, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.healthErr != nil {
		return nil, s.healthErr
	}
	if s.health != nil {
		h := *s.health
		return &h, nil
	}
	return &models.ClusterHealth{
		ClusterID:    clusterID,
		ClusterState: models.ClusterStateHealthy,
		Servers:      []*models.ClusterServerHealth{},
	}, nil
}

func (s *mockStore) ListenConfigChanges(ctx context.Context, channel string) (<-chan string, func() error, error) {
	if s.listenErr != nil {
		return nil, nil, s.listenErr
	}
	ch := make(chan string)
	return ch, func() error { return nil }, nil
}

func (s *mockStore) heartbeatCount(serverID string) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.heartbeats[serverID]
}
