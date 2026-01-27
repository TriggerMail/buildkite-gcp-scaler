package buildkite

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"

	"github.com/buildkite/go-buildkite/v4"
	hclog "github.com/hashicorp/go-hclog"
)

type Client struct {
	OrgSlug         string
	BuildkiteClient *buildkite.Client
	Logger          hclog.Logger
	AgentToken      string
}

type AgentMetrics struct {
	OrgSlug       string
	Queue         string
	Cluster       string
	ScheduledJobs int64
	RunningJobs   int64
	WaitingJobs   int64
	IdleAgents    int64
	BusyAgents    int64
	TotalAgents   int64
}

func (c *Client) GetAgentMetrics(ctx context.Context, queue string, cluster string) (*AgentMetrics, error) {
	url := "https://agent.buildkite.com/v3/metrics"
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", fmt.Sprintf("Token %s", c.AgentToken))
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to call metrics API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return nil, fmt.Errorf("metrics API returned status %d: %s", resp.StatusCode, string(body))
	}

	var apiResp struct {
		Organization struct {
			Slug string `json:"slug"`
		} `json:"organization"`
		Cluster struct {
			Name string `json:"name"`
		} `json:"cluster"`
		Agents struct {
			Idle   int64 `json:"idle"`
			Busy   int64 `json:"busy"`
			Total  int64 `json:"total"`
			Queues map[string]struct {
				Idle  int64 `json:"idle"`
				Busy  int64 `json:"busy"`
				Total int64 `json:"total"`
			} `json:"queues"`
		} `json:"agents"`
		Jobs struct {
			Scheduled int64 `json:"scheduled"`
			Running   int64 `json:"running"`
			Waiting   int64 `json:"waiting"`
			Total     int64 `json:"total"`
			Queues    map[string]struct {
				Scheduled int64 `json:"scheduled"`
				Running   int64 `json:"running"`
				Waiting   int64 `json:"waiting"`
				Total     int64 `json:"total"`
			} `json:"queues"`
		} `json:"jobs"`
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %w", err)
	}
	if err := json.Unmarshal(body, &apiResp); err != nil {
		return nil, fmt.Errorf("failed to parse metrics response: %w", err)
	}

	// Extract queue-specific metrics
	queueJobs := apiResp.Jobs.Queues[queue]
	queueAgents := apiResp.Agents.Queues[queue]

	return &AgentMetrics{
		OrgSlug:       apiResp.Organization.Slug,
		Queue:         queue,
		Cluster:       apiResp.Cluster.Name,
		ScheduledJobs: queueJobs.Scheduled,
		RunningJobs:   queueJobs.Running,
		WaitingJobs:   queueJobs.Waiting,
		IdleAgents:    queueAgents.Idle,
		BusyAgents:    queueAgents.Busy,
		TotalAgents:   queueAgents.Total,
	}, nil
}

func NewClient(org string, agentToken string, logger hclog.Logger) (*Client, error) {
	client, err := buildkite.NewOpts(buildkite.WithTokenAuth(agentToken))
	if err != nil {
		return nil, fmt.Errorf("failed to create Buildkite client: %w", err)
	}

	return &Client{
		BuildkiteClient: client,
		Logger:          logger.Named("bkapi"),
		OrgSlug:         org,
		AgentToken:      agentToken,
	}, nil
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

			// Check character before the match (if exists) is not alphanumeric
			// This prevents "myqueue=prod" from matching when looking for "queue=prod"
			if idx > 0 {
				prevChar := queryRule[idx-1]
				// If previous character is alphanumeric, underscore, or hyphen, it's not an exact match
				if (prevChar >= 'a' && prevChar <= 'z') ||
					(prevChar >= 'A' && prevChar <= 'Z') ||
					(prevChar >= '0' && prevChar <= '9') ||
					prevChar == '_' || prevChar == '-' {
					continue
				}
			}

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
