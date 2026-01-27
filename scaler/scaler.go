package scaler

import (
	"context"
	"sync"
	"time"

	"github.com/DataDog/datadog-go/statsd"
	"github.com/TriggerMail/buildkite-gcp-scaler/pkg/buildkite"
	"github.com/TriggerMail/buildkite-gcp-scaler/pkg/gce"
	hclog "github.com/hashicorp/go-hclog"
	"github.com/hashicorp/go-multierror"
)

type Config struct {
	Datadog               string
	OrgSlug               string
	GCPProject            string
	GCPZone               string
	InstanceGroupName     string
	InstanceGroupTemplate string
	BuildkiteQueue        string
	BuildkiteCluster      string
	BuildkiteToken        string
	Concurrency           int
	MaxRunDuration        int64
	PollInterval          *time.Duration
	AgentsPerInstance     int64 // Number of agents each VM instance spawns (default: 1)
	MinInstances          int64 // Minimum number of VM instances to keep running
}

type StatsdClient interface {
	Gauge(name string, value float64, tags []string, rate float64) error
	// Add other methods you use, e.g. Count, Timing, etc.
}

type Scaler struct {
	cfg *Config

	gce interface {
		LiveInstanceCount(ctx context.Context, projectID, zone, instanceGroupName string) (int64, error)
		LaunchInstanceForGroup(ctx context.Context, projectID, zone, groupName, templateName string, maxRunDuration int64) error
	}

	buildkite interface {
		GetAgentMetrics(context.Context, string, string) (*buildkite.AgentMetrics, error)
	}

	logger hclog.Logger

	Statsd StatsdClient
}

func NewAutoscaler(cfg *Config, logger hclog.Logger) (*Scaler, error) {
	client, err := gce.NewClient(logger)
	if err != nil {
		return nil, err
	}

	loggerFields := []interface{}{"queue", cfg.BuildkiteQueue}
	if cfg.BuildkiteCluster != "" {
		loggerFields = append(loggerFields, "cluster", cfg.BuildkiteCluster)
	} else {
		loggerFields = append(loggerFields, "cluster", "unclustered")
	}

	bkClient, err := buildkite.NewClient(cfg.OrgSlug, cfg.BuildkiteToken, logger)
	if err != nil {
		return nil, err
	}

	scaler := Scaler{
		cfg:       cfg,
		logger:    logger.Named("scaler").With(loggerFields...),
		buildkite: bkClient,
		gce:       client,
	}

	if cfg.Datadog != "" {
		s, err := statsd.New(cfg.Datadog)
		if err != nil {
			return nil, err
		}
		scaler.Statsd = s
	}

	return &scaler, nil
}

func (s *Scaler) Run(ctx context.Context) error {
	ticker := time.NewTimer(0)
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-ticker.C:
			sem := make(chan int, s.cfg.Concurrency)
			if err := s.run(ctx, &sem); err != nil {
				s.logger.Error("Autoscaling failed", "error", err)
			}
			close(sem)

			if s.cfg.PollInterval != nil {
				ticker.Reset(*s.cfg.PollInterval)
			} else {
				return nil
			}
		}
	}
}

func (s *Scaler) run(ctx context.Context, sem *chan int) error {
	metrics, err := s.buildkite.GetAgentMetrics(ctx, s.cfg.BuildkiteQueue, s.cfg.BuildkiteCluster)
	if err != nil {
		return err
	}

	s.Statsd.Gauge("buildkite-gcp-autoscaler.scheduled_jobs", float64(metrics.ScheduledJobs), []string{}, 1)
	s.Statsd.Gauge("buildkite-gcp-autoscaler.running_jobs", float64(metrics.RunningJobs), []string{}, 1)
	s.Statsd.Gauge("buildkite-gcp-autoscaler.waiting_jobs", float64(metrics.WaitingJobs), []string{}, 1)

	totalJobDemand := metrics.ScheduledJobs + metrics.RunningJobs + metrics.WaitingJobs

	// Get current VM count
	liveInstanceCount, err := s.gce.LiveInstanceCount(ctx, s.cfg.GCPProject, s.cfg.GCPZone, s.cfg.InstanceGroupName)
	s.Statsd.Gauge("buildkite-gcp-autoscaler.live_instance", float64(liveInstanceCount), []string{}, 1)
	if err != nil {
		return err
	}

	// Calculate agent capacity: VMs * agents per VM
	agentsPerInstance := s.cfg.AgentsPerInstance
	if agentsPerInstance <= 0 {
		agentsPerInstance = 1 // Default to 1 agent per instance if not configured
	}
	currentAgentCapacity := liveInstanceCount * agentsPerInstance

	// Calculate how many instances we need based on job demand and agents per instance
	// Round up division: (jobs + agentsPerInstance - 1) / agentsPerInstance
	requiredInstances := (totalJobDemand + agentsPerInstance - 1) / agentsPerInstance
	if requiredInstances < s.cfg.MinInstances {
		requiredInstances = s.cfg.MinInstances // Always maintain at least minInstances
	}

	s.logger.Debug("scaling decision",
		"liveInstances", liveInstanceCount,
		"agentsPerInstance", agentsPerInstance,
		"currentAgentCapacity", currentAgentCapacity,
		"totalJobDemand", totalJobDemand,
		"requiredInstances", requiredInstances,
		"scheduled", metrics.ScheduledJobs,
		"running", metrics.RunningJobs,
		"waiting", metrics.WaitingJobs,
		"minInstances", s.cfg.MinInstances)

	// Check if we have enough capacity
	if currentAgentCapacity >= totalJobDemand && liveInstanceCount >= s.cfg.MinInstances {
		s.logger.Debug("no scaling needed",
			"currentAgentCapacity", currentAgentCapacity,
			"totalJobDemand", totalJobDemand,
			"liveInstances", liveInstanceCount)
		return nil
	}

	// Calculate how many more instances we need
	required := requiredInstances - liveInstanceCount
	if required <= 0 {
		required = s.cfg.MinInstances // Ensure at least minInstances if we got here
	}

	s.logger.Info("scaling up",
		"liveInstances", liveInstanceCount,
		"currentAgentCapacity", currentAgentCapacity,
		"required", required,
		"totalJobDemand", totalJobDemand,
		"agentsPerInstance", agentsPerInstance,
		"minInstances", s.cfg.MinInstances)

	errChan := make(chan error, required) // Buffer for all possible errors
	wg := new(sync.WaitGroup)

	// Launch instances concurrently
	wg.Add(int(required))
	for i := int64(0); i < required; i++ {
		go func() {
			defer wg.Done()

			// Check if context is already cancelled before acquiring semaphore
			select {
			case <-ctx.Done():
				s.logger.Debug("context cancelled, skipping instance launch")
				return
			default:
			}

			*sem <- 1
			defer func() { <-*sem }()

			// Check again after acquiring semaphore
			if ctx.Err() != nil {
				s.logger.Debug("context cancelled after acquiring semaphore")
				return
			}

			if err := s.gce.LaunchInstanceForGroup(ctx, s.cfg.GCPProject, s.cfg.GCPZone, s.cfg.InstanceGroupName, s.cfg.InstanceGroupTemplate, s.cfg.MaxRunDuration); err != nil {
				errChan <- err
				s.logger.Error("Failed to launch instance", "error", err)
			}
		}()
	}

	// Wait for all goroutines to complete
	wg.Wait()
	close(errChan)

	// Collect all errors that occurred
	var result error
	for err := range errChan {
		result = multierror.Append(result, err)
	}

	return result
}
