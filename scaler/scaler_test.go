package scaler

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/DataDog/datadog-go/statsd"
	"github.com/TriggerMail/buildkite-gcp-scaler/pkg/buildkite"
	hclog "github.com/hashicorp/go-hclog"
	"github.com/stretchr/testify/assert"
)

// --- Mocks ---

type mockBuildkite struct {
	metrics *buildkite.AgentMetrics
	err     error
}

func (m *mockBuildkite) GetAgentMetrics(ctx context.Context, queue string) (*buildkite.AgentMetrics, error) {
	return m.metrics, m.err
}

type mockGCE struct {
	liveCount int64
	liveErr   error

	launchErr error
	launches  int
	mu        sync.Mutex
}

func (m *mockGCE) LiveInstanceCount(ctx context.Context, projectID, zone, instanceGroupName string) (int64, error) {
	return m.liveCount, m.liveErr
}

func (m *mockGCE) LaunchInstanceForGroup(ctx context.Context, projectID, zone, groupName, templateName string, maxRunDuration int64) error {
	m.mu.Lock()
	m.launches++
	m.mu.Unlock()
	return m.launchErr
}

type mockStatsd struct {
	statsd.NoOpClient
	calls []string
	mu    sync.Mutex
}

func (m *mockStatsd) Gauge(name string, value float64, tags []string, rate float64) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.calls = append(m.calls, name)
	return nil
}

// --- Tests ---

func TestScalerRun_NoScalingNeeded(t *testing.T) {
	ctx := context.Background()
	sem := make(chan int, 2)

	bk := &mockBuildkite{
		metrics: &buildkite.AgentMetrics{
			ScheduledJobs: 2,
			RunningJobs:   2,
		},
	}
	gce := &mockGCE{
		liveCount: 4,
	}
	stats := &mockStatsd{}
	logger := hclog.NewNullLogger()

	scaler := &Scaler{
		cfg: &Config{
			GCPProject:            "proj",
			GCPZone:               "zone",
			InstanceGroupName:     "group",
			InstanceGroupTemplate: "tmpl",
			MaxRunDuration:        60,
		},
		buildkite: bk,
		gce:       gce,
		logger:    logger,
		Statsd:    stats,
	}

	err := scaler.run(ctx, &sem)
	assert.NoError(t, err)
	assert.Equal(t, 0, gce.launches)
}

func TestScalerRun_ScalingNeeded(t *testing.T) {
	ctx := context.Background()
	sem := make(chan int, 2)

	bk := &mockBuildkite{
		metrics: &buildkite.AgentMetrics{
			ScheduledJobs: 3,
			RunningJobs:   2,
		},
	}
	gce := &mockGCE{
		liveCount: 2,
	}
	stats := &mockStatsd{}
	logger := hclog.NewNullLogger()

	scaler := &Scaler{
		cfg: &Config{
			GCPProject:            "proj",
			GCPZone:               "zone",
			InstanceGroupName:     "group",
			InstanceGroupTemplate: "tmpl",
			MaxRunDuration:        60,
		},
		buildkite: bk,
		gce:       gce,
		logger:    logger,
		Statsd:    stats,
	}

	err := scaler.run(ctx, &sem)
	assert.NoError(t, err)
	// Should launch 3 instances (5 needed - 2 live)
	assert.Equal(t, 3, gce.launches)
}

func TestScalerRun_AlwaysAtLeastOneInstance(t *testing.T) {
	ctx := context.Background()
	sem := make(chan int, 2)

	bk := &mockBuildkite{
		metrics: &buildkite.AgentMetrics{
			ScheduledJobs: 0,
			RunningJobs:   0,
		},
	}
	gce := &mockGCE{
		liveCount: 0,
	}
	stats := &mockStatsd{}
	logger := hclog.NewNullLogger()

	scaler := &Scaler{
		cfg: &Config{
			GCPProject:            "proj",
			GCPZone:               "zone",
			InstanceGroupName:     "group",
			InstanceGroupTemplate: "tmpl",
			MaxRunDuration:        60,
		},
		buildkite: bk,
		gce:       gce,
		logger:    logger,
		Statsd:    stats,
	}

	err := scaler.run(ctx, &sem)
	assert.NoError(t, err)
	// Should launch at least 1 instance
	assert.Equal(t, 1, gce.launches)
}

func TestScalerRun_BuildkiteError(t *testing.T) {
	ctx := context.Background()
	sem := make(chan int, 2)

	bk := &mockBuildkite{
		err: errors.New("buildkite error"),
	}
	gce := &mockGCE{}
	stats := &mockStatsd{}
	logger := hclog.NewNullLogger()

	scaler := &Scaler{
		cfg:       &Config{},
		buildkite: bk,
		gce:       gce,
		logger:    logger,
		Statsd:    stats,
	}

	err := scaler.run(ctx, &sem)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "buildkite error")
}

func TestScalerRun_GCECountError(t *testing.T) {
	ctx := context.Background()
	sem := make(chan int, 2)

	bk := &mockBuildkite{
		metrics: &buildkite.AgentMetrics{
			ScheduledJobs: 1,
			RunningJobs:   1,
		},
	}
	gce := &mockGCE{
		liveErr: errors.New("gce count error"),
	}
	stats := &mockStatsd{}
	logger := hclog.NewNullLogger()

	scaler := &Scaler{
		cfg:       &Config{},
		buildkite: bk,
		gce:       gce,
		logger:    logger,
		Statsd:    stats,
	}

	err := scaler.run(ctx, &sem)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "gce count error")
}

func TestScalerRun_LaunchInstanceError(t *testing.T) {
	ctx := context.Background()
	sem := make(chan int, 2)

	bk := &mockBuildkite{
		metrics: &buildkite.AgentMetrics{
			ScheduledJobs: 2,
			RunningJobs:   0,
		},
	}
	gce := &mockGCE{
		liveCount: 0,
		launchErr: errors.New("launch error"),
	}
	stats := &mockStatsd{}
	logger := hclog.NewNullLogger()

	scaler := &Scaler{
		cfg: &Config{
			GCPProject:            "proj",
			GCPZone:               "zone",
			InstanceGroupName:     "group",
			InstanceGroupTemplate: "tmpl",
			MaxRunDuration:        60,
		},
		buildkite: bk,
		gce:       gce,
		logger:    logger,
		Statsd:    stats,
	}

	err := scaler.run(ctx, &sem)
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "launch error")
	assert.Equal(t, 2, gce.launches)
}

func TestScalerRun_ConcurrencyRespected(t *testing.T) {
	ctx := context.Background()
	sem := make(chan int, 1) // Only 1 concurrent launch allowed

	bk := &mockBuildkite{
		metrics: &buildkite.AgentMetrics{
			ScheduledJobs: 3,
			RunningJobs:   0,
		},
	}
	gce := &mockGCE{
		liveCount: 0,
	}
	stats := &mockStatsd{}
	logger := hclog.NewNullLogger()

	scaler := &Scaler{
		cfg: &Config{
			GCPProject:            "proj",
			GCPZone:               "zone",
			InstanceGroupName:     "group",
			InstanceGroupTemplate: "tmpl",
			MaxRunDuration:        60,
		},
		buildkite: bk,
		gce:       gce,
		logger:    logger,
		Statsd:    stats,
	}

	done := make(chan struct{})
	go func() {
		_ = scaler.run(ctx, &sem)
		close(done)
	}()

	select {
	case <-done:
		// test finished
	case <-time.After(2 * time.Second):
		t.Fatal("run did not finish in time")
	}
	assert.Equal(t, 3, gce.launches)
}
