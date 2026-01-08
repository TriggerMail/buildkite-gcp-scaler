package scaler

import (
	"context"
	"math"
	"sync"
	"time"

	"github.com/DataDog/datadog-go/statsd"
	"github.com/TriggerMail/buildkite-gcp-scaler/pkg/buildkite"
	"github.com/TriggerMail/buildkite-gcp-scaler/pkg/gce"
	hclog "github.com/hashicorp/go-hclog"
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

	scaler := Scaler{
		cfg:       cfg,
		logger:    logger.Named("scaler").With(loggerFields...),
		buildkite: buildkite.NewClient(cfg.OrgSlug, cfg.BuildkiteToken, logger),
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

	totalInstanceRequirement := metrics.ScheduledJobs + metrics.RunningJobs

	liveInstanceCount, err := s.gce.LiveInstanceCount(ctx, s.cfg.GCPProject, s.cfg.GCPZone, s.cfg.InstanceGroupName)
	s.Statsd.Gauge("buildkite-gcp-autoscaler.live_instance", float64(liveInstanceCount), []string{}, 1)
	if err != nil {
		return err
	}

	s.logger.Debug("scaling decision", "liveInstances", liveInstanceCount, "totalRequired", totalInstanceRequirement, "scheduled", metrics.ScheduledJobs, "running", metrics.RunningJobs)

	if liveInstanceCount >= totalInstanceRequirement && liveInstanceCount >= 1 {
		s.logger.Debug("no scaling needed", "liveInstances", liveInstanceCount, "totalRequired", totalInstanceRequirement)
		return nil
	}
	// required equals total instance needs minus liveinstance count, or the max of 1 to ensure there's always at least one instance availble
	required := int64(math.Max(float64(totalInstanceRequirement-liveInstanceCount), 1))
	s.logger.Info("scaling up", "liveInstances", liveInstanceCount, "required", required, "totalRequired", totalInstanceRequirement)

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

	// Return the first error if any occurred
	select {
	case err := <-errChan:
		return err
	default:
		return nil
	}
}
