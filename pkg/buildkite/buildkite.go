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

	metrics := AgentMetrics{
		OrgSlug: c.OrgSlug,
		Queue:   queue,
		Cluster: cluster,
	}

	// Paginate through all builds with running/scheduled state
	opts := &buildkite.BuildsListOptions{
		State: []string{"running", "scheduled"},
		ListOptions: buildkite.ListOptions{
			PerPage: 100, // Fetch more per page to reduce API calls
		},
	}

	pageNum := 1
	for {
		c.Logger.Debug("fetching builds page", "page", pageNum)

		builds, resp, err := c.BuildkiteClient.Builds.ListByOrg(ctx, c.OrgSlug, opts)
		if err != nil {
			return nil, fmt.Errorf("failed to list builds (page %d): %w", pageNum, err)
		}

		c.Logger.Debug("processing builds", "count", len(builds), "page", pageNum)

		// Process builds on this page
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

		// Check if there are more pages
		if resp.NextPage == 0 {
			break
		}

		// Move to next page
		opts.Page = resp.NextPage
		pageNum++
	}

	c.Logger.Debug("Retrieved agent metrics", "scheduled", metrics.ScheduledJobs, "running", metrics.RunningJobs, "cluster", cluster, "pages", pageNum)
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
	// Queue can appear in different formats:
	// - "queue=deploy" (standalone)
	// - "os=linux AND queue=deploy" (with preceding rules)
	// - "queue=deploy AND arch=amd64" (with trailing rules)
	// We need to match the exact queue name, not substrings
	targetQueue := fmt.Sprintf("queue=%s", queue)
	for _, queryRule := range job.AgentQueryRules {
		// Check if the rule contains our target queue
		if strings.Contains(queryRule, targetQueue) {
			// Verify it's an exact match by checking boundaries
			idx := strings.Index(queryRule, targetQueue)
			endIdx := idx + len(targetQueue)

			// Check character after the match (if exists) is not alphanumeric
			// This prevents "queue=deploy" from matching "queue=deployment"
			if endIdx < len(queryRule) {
				nextChar := queryRule[endIdx]
				// If next character is alphanumeric, underscore, or hyphen, it's not an exact match
				if (nextChar >= 'a' && nextChar <= 'z') ||
					(nextChar >= 'A' && nextChar <= 'Z') ||
					(nextChar >= '0' && nextChar <= '9') ||
					nextChar == '_' || nextChar == '-' {
					continue
				}
			}
			return true
		}
	}

	return false
}
