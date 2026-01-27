package buildkite

import (
	"testing"

	"github.com/buildkite/go-buildkite/v4"
	hclog "github.com/hashicorp/go-hclog"
)

func TestJobMatchesCriteria(t *testing.T) {
	logger := hclog.NewNullLogger()
	client := &Client{
		OrgSlug:    "test-org",
		Logger:     logger,
		AgentToken: "dummy-token",
	}

	tests := []struct {
		name          string
		job           *buildkite.Job
		queue         string
		cluster       string
		expectedMatch bool
		description   string
	}{
		{
			name: "clustered job matches cluster ID and queue",
			job: &buildkite.Job{
				ClusterID:       "cluster-123",
				AgentQueryRules: []string{"queue=deploy"},
			},
			queue:         "deploy",
			cluster:       "cluster-123",
			expectedMatch: true,
			description:   "Job in cluster-123 with queue=deploy should match when filtering for cluster-123 and deploy queue",
		},
		{
			name: "clustered job does not match different cluster ID",
			job: &buildkite.Job{
				ClusterID:       "cluster-123",
				AgentQueryRules: []string{"queue=deploy"},
			},
			queue:         "deploy",
			cluster:       "cluster-456",
			expectedMatch: false,
			description:   "Job in cluster-123 should not match when filtering for cluster-456",
		},
		{
			name: "clustered job does not match when filtering for unclustered",
			job: &buildkite.Job{
				ClusterID:       "cluster-123",
				AgentQueryRules: []string{"queue=deploy"},
			},
			queue:         "deploy",
			cluster:       "",
			expectedMatch: false,
			description:   "Clustered job should not match when filtering for unclustered jobs (empty cluster param)",
		},
		{
			name: "unclustered job matches when no cluster specified",
			job: &buildkite.Job{
				ClusterID:       "",
				AgentQueryRules: []string{"queue=build"},
			},
			queue:         "build",
			cluster:       "",
			expectedMatch: true,
			description:   "Unclustered job with queue=build should match when filtering for unclustered jobs",
		},
		{
			name: "unclustered job does not match when cluster is specified",
			job: &buildkite.Job{
				ClusterID:       "",
				AgentQueryRules: []string{"queue=build"},
			},
			queue:         "build",
			cluster:       "cluster-123",
			expectedMatch: false,
			description:   "Unclustered job should not match when filtering for a specific cluster",
		},
		{
			name: "job does not match different queue",
			job: &buildkite.Job{
				ClusterID:       "cluster-123",
				AgentQueryRules: []string{"queue=deploy"},
			},
			queue:         "build",
			cluster:       "cluster-123",
			expectedMatch: false,
			description:   "Job with queue=deploy should not match when filtering for queue=build",
		},
		{
			name: "job matches with multiple query rules",
			job: &buildkite.Job{
				ClusterID:       "cluster-123",
				AgentQueryRules: []string{"os=linux", "queue=deploy", "arch=amd64"},
			},
			queue:         "deploy",
			cluster:       "cluster-123",
			expectedMatch: true,
			description:   "Job with multiple query rules including queue=deploy should match",
		},
		{
			name: "job with queue substring does not match",
			job: &buildkite.Job{
				ClusterID:       "cluster-123",
				AgentQueryRules: []string{"queue=deployment"},
			},
			queue:         "deploy",
			cluster:       "cluster-123",
			expectedMatch: false,
			description:   "Job with queue=deployment should not match when filtering for queue=deploy",
		},
		{
			name: "job with empty query rules matches oneshot queue",
			job: &buildkite.Job{
				ClusterID:       "",
				AgentQueryRules: []string{},
			},
			queue:         "oneshot",
			cluster:       "",
			expectedMatch: true,
			description:   "Job with no agent query rules should match the 'oneshot' queue",
		},
		{
			name: "job with empty query rules does not match non-default queue",
			job: &buildkite.Job{
				ClusterID:       "cluster-123",
				AgentQueryRules: []string{},
			},
			queue:         "deploy",
			cluster:       "cluster-123",
			expectedMatch: false,
			description:   "Job with no agent query rules should not match a non-default queue",
		},
		{
			name: "job with nil query rules matches oneshot queue",
			job: &buildkite.Job{
				ClusterID:       "",
				AgentQueryRules: nil,
			},
			queue:         "oneshot",
			cluster:       "",
			expectedMatch: true,
			description:   "Job with nil agent query rules should match the 'oneshot' queue",
		},
		{
			name: "case sensitive queue matching",
			job: &buildkite.Job{
				ClusterID:       "cluster-123",
				AgentQueryRules: []string{"queue=Deploy"},
			},
			queue:         "deploy",
			cluster:       "cluster-123",
			expectedMatch: false,
			description:   "Queue matching should be case-sensitive (queue=Deploy != queue=deploy)",
		},
		{
			name: "queue as part of larger rule string",
			job: &buildkite.Job{
				ClusterID:       "cluster-123",
				AgentQueryRules: []string{"os=linux AND queue=deploy AND arch=amd64"},
			},
			queue:         "deploy",
			cluster:       "cluster-123",
			expectedMatch: true,
			description:   "Job with queue=deploy in a complex rule should match",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := client.jobMatchesCriteria(tt.job, tt.queue, tt.cluster)
			if result != tt.expectedMatch {
				t.Errorf("%s\nExpected match=%v, got=%v\nJob: ClusterID=%q, AgentQueryRules=%v\nFilter: queue=%q, cluster=%q",
					tt.description,
					tt.expectedMatch,
					result,
					tt.job.ClusterID,
					tt.job.AgentQueryRules,
					tt.queue,
					tt.cluster,
				)
			}
		})
	}
}

// TestJobMatchesCriteria_EdgeCases tests edge cases and boundary conditions
func TestJobMatchesCriteria_EdgeCases(t *testing.T) {
	logger := hclog.NewNullLogger()
	client := &Client{
		OrgSlug:    "test-org",
		Logger:     logger,
		AgentToken: "dummy-token",
	}

	t.Run("empty queue name", func(t *testing.T) {
		job := &buildkite.Job{
			ClusterID:       "cluster-123",
			AgentQueryRules: []string{"queue="},
		}
		result := client.jobMatchesCriteria(job, "", "cluster-123")
		if !result {
			t.Error("Should match when both job and filter have empty queue")
		}
	})

	t.Run("special characters in queue name", func(t *testing.T) {
		job := &buildkite.Job{
			ClusterID:       "cluster-123",
			AgentQueryRules: []string{"queue=deploy-prod-us-east-1"},
		}
		result := client.jobMatchesCriteria(job, "deploy-prod-us-east-1", "cluster-123")
		if !result {
			t.Error("Should match queue names with hyphens")
		}
	})

	t.Run("queue name with prefix does not match", func(t *testing.T) {
		job := &buildkite.Job{
			ClusterID:       "cluster-123",
			AgentQueryRules: []string{"myqueue=prod"},
		}
		result := client.jobMatchesCriteria(job, "prod", "cluster-123")
		if result {
			t.Error("Should not match 'myqueue=prod' when filtering for queue=prod")
		}
	})

	t.Run("production does not match prod queue", func(t *testing.T) {
		job := &buildkite.Job{
			ClusterID:       "cluster-123",
			AgentQueryRules: []string{"queue=production"},
		}
		result := client.jobMatchesCriteria(job, "prod", "cluster-123")
		if result {
			t.Error("Should not match 'queue=production' when filtering for queue=prod")
		}
	})

	t.Run("UUID cluster ID", func(t *testing.T) {
		clusterID := "f801dad2-e7cc-46fc-aaa2-a8b000174d9b"
		job := &buildkite.Job{
			ClusterID:       clusterID,
			AgentQueryRules: []string{"queue=oneshot"},
		}
		result := client.jobMatchesCriteria(job, "oneshot", clusterID)
		if !result {
			t.Error("Should match UUID format cluster IDs")
		}
	})
}
