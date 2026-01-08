package buildkite

import (
	"context"
	"fmt"
	"strings"

	"github.com/buildkite/go-buildkite/v4"
	hclog "github.com/hashicorp/go-hclog"
)

type Client struct {
	OrgSlug         string
	BuildkiteClient *buildkite.Client
	Logger          hclog.Logger
}

func NewClient(org string, agentToken string, logger hclog.Logger) *Client {
	client, err := buildkite.NewOpts(buildkite.WithTokenAuth(agentToken))
	if err != nil {
		logger.Error("Failed to create Buildkite client", "error", err)
		return nil
	}

	return &Client{
		BuildkiteClient: client,
		Logger:          logger.Named("bkapi"),
		OrgSlug:         org,
	}
}

type AgentMetrics struct {
	OrgSlug       string
	Queue         string
	Cluster       string
	ScheduledJobs int64
	RunningJobs   int64
}

func (c *Client) GetAgentMetrics(ctx context.Context, queue string, cluster string) (*AgentMetrics, error) {
	c.Logger.Debug("Collecting agent metrics", "queue", queue, "cluster", cluster)

	// List builds with the specified state filters
	builds, _, err := c.BuildkiteClient.Builds.ListByOrg(ctx, c.OrgSlug, &buildkite.BuildsListOptions{
		State: []string{"running", "scheduled"},
	})

	if err != nil {
		return nil, err
	}

	metrics := AgentMetrics{
		OrgSlug: c.OrgSlug,
		Queue:   queue,
		Cluster: cluster,
	}

	// iterate over all builds and count the number of scheduled and running jobs
	for _, build := range builds {
		for _, job := range build.Jobs {
			// Check if job matches the criteria
			if c.jobMatchesCriteria(&job, queue, cluster) {
				if job.State == "scheduled" {
					metrics.ScheduledJobs++
				}
				if job.State == "running" {
					metrics.RunningJobs++
				}
			}
		}
	}

	c.Logger.Debug("Retrieved agent metrics", "scheduled", metrics.ScheduledJobs, "running", metrics.RunningJobs, "cluster", cluster)
	return &metrics, nil
}

// jobMatchesCriteria checks if a job matches the queue and cluster criteria
func (c *Client) jobMatchesCriteria(job *buildkite.Job, queue string, cluster string) bool {
	// First check the cluster criteria
	if cluster != "" {
		// We want a specific cluster - job must match that cluster ID
		if job.ClusterID != cluster {
			return false
		}
	} else {
		// No cluster specified - we only want unclustered jobs
		// If the job has a ClusterID, it's clustered, so skip it
		if job.ClusterID != "" {
			return false
		}
	}

	// Cluster criteria matches, now check queue in AgentQueryRules
	targetQueue := fmt.Sprintf("queue=%s", queue)
	for _, queryRule := range job.AgentQueryRules {
		if strings.Contains(queryRule, targetQueue) {
			return true
		}
	}

	return false
}
