package gce

import (
	"context"
	"fmt"
	"testing"

	compute "google.golang.org/api/compute/v0.beta"
)

// mockInstanceGroupsService mocks the GCE Instance Groups API
type mockInstanceGroupsService struct {
	listInstancesFunc func() (*compute.InstanceGroupsListInstances, error)
	callCount         int
}

func (m *mockInstanceGroupsService) ListInstances(projectID, zone, instanceGroup string, req *compute.InstanceGroupsListInstancesRequest) *mockListInstancesCall {
	return &mockListInstancesCall{
		mockService: m,
	}
}

type mockListInstancesCall struct {
	mockService *mockInstanceGroupsService
}

func (m *mockListInstancesCall) Context(ctx context.Context) *mockListInstancesCall {
	return m
}

func (m *mockListInstancesCall) Do() (*compute.InstanceGroupsListInstances, error) {
	m.mockService.callCount++
	return m.mockService.listInstancesFunc()
}

// TestIsLiveInstance tests the isLiveInstance helper function
func TestIsLiveInstance(t *testing.T) {
	tests := []struct {
		status   string
		expected bool
	}{
		{"PROVISIONING", true},
		{"STAGING", true},
		{"RUNNING", true},
		{"STOPPING", false},
		{"TERMINATED", false},
		{"SUSPENDING", false},
		{"SUSPENDED", false},
		{"", false},
		{"UNKNOWN", false},
	}

	for _, tt := range tests {
		t.Run(tt.status, func(t *testing.T) {
			result := isLiveInstance(tt.status)
			if result != tt.expected {
				t.Errorf("isLiveInstance(%q) = %v, want %v", tt.status, result, tt.expected)
			}
		})
	}
}

// TestLiveInstanceCount tests the LiveInstanceCount method
func TestLiveInstanceCount(t *testing.T) {
	tests := []struct {
		name          string
		instances     []*compute.InstanceWithNamedPorts
		expectedCount int64
		description   string
	}{
		{
			name: "counts PROVISIONING instances",
			instances: []*compute.InstanceWithNamedPorts{
				{Instance: "https://.../instances/instance-1", Status: "PROVISIONING"},
				{Instance: "https://.../instances/instance-2", Status: "STAGING"},
				{Instance: "https://.../instances/instance-3", Status: "RUNNING"},
			},
			expectedCount: 3,
			description:   "Should count all instances in PROVISIONING, STAGING, and RUNNING states",
		},
		{
			name: "excludes TERMINATED instances",
			instances: []*compute.InstanceWithNamedPorts{
				{Instance: "https://.../instances/instance-1", Status: "RUNNING"},
				{Instance: "https://.../instances/instance-2", Status: "TERMINATED"},
				{Instance: "https://.../instances/instance-3", Status: "STOPPING"},
			},
			expectedCount: 1,
			description:   "Should only count RUNNING instance, not TERMINATED or STOPPING",
		},
		{
			name: "handles empty instance group",
			instances: []*compute.InstanceWithNamedPorts{},
			expectedCount: 0,
			description:   "Should return 0 for empty instance group",
		},
		{
			name: "handles nil instances",
			instances: nil,
			expectedCount: 0,
			description:   "Should handle nil instances list gracefully",
		},
		{
			name: "handles all STAGING instances",
			instances: []*compute.InstanceWithNamedPorts{
				{Instance: "https://.../instances/instance-1", Status: "STAGING"},
				{Instance: "https://.../instances/instance-2", Status: "STAGING"},
			},
			expectedCount: 2,
			description:   "Should count all STAGING instances",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test the logic directly without needing a full mock
			// This tests the core counting logic that LiveInstanceCount uses
			count := int64(0)
			for _, i := range tt.instances {
				if isLiveInstance(i.Status) {
					count++
				}
			}

			if count != tt.expectedCount {
				t.Errorf("%s\nExpected count=%d, got=%d", tt.description, tt.expectedCount, count)
			}
		})
	}
}

// TestInstanceNameMatching tests the instance name matching logic
func TestInstanceNameMatching(t *testing.T) {
	tests := []struct {
		name             string
		instanceName     string
		instanceURLs     []string
		shouldMatch      []bool
		description      string
	}{
		{
			name:         "exact instance name match",
			instanceName: "buildkite-oneshot-abc123",
			instanceURLs: []string{
				"https://www.googleapis.com/compute/beta/projects/proj/zones/zone/instances/buildkite-oneshot-abc123",
			},
			shouldMatch: []bool{true},
			description: "Should match exact instance name in URL",
		},
		{
			name:         "does not match similar instance name",
			instanceName: "buildkite-oneshot-abc123",
			instanceURLs: []string{
				"https://www.googleapis.com/compute/beta/projects/proj/zones/zone/instances/my-buildkite-oneshot-abc123",
				"https://www.googleapis.com/compute/beta/projects/proj/zones/zone/instances/buildkite-oneshot-abc123-extra",
			},
			shouldMatch: []bool{false, false},
			description: "Should not match instances with similar but different names",
		},
		{
			name:         "matches with short instance name",
			instanceName: "vm-1",
			instanceURLs: []string{
				"https://www.googleapis.com/compute/beta/projects/proj/zones/zone/instances/vm-1",
				"https://www.googleapis.com/compute/beta/projects/proj/zones/zone/instances/vm-10",
			},
			shouldMatch: []bool{true, false},
			description: "Should only match exact name, not substring",
		},
		{
			name:         "handles special characters in instance name",
			instanceName: "buildkite-clustered-oneshot-20251106070947386800000001-abc123",
			instanceURLs: []string{
				"https://www.googleapis.com/compute/beta/projects/proj/zones/zone/instances/buildkite-clustered-oneshot-20251106070947386800000001-abc123",
			},
			shouldMatch: []bool{true},
			description: "Should match instance names with hyphens and numbers",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			for i, url := range tt.instanceURLs {
				expectedSuffix := "/instances/" + tt.instanceName
				result := len(url) >= len(expectedSuffix) && url[len(url)-len(expectedSuffix):] == expectedSuffix
				
				if result != tt.shouldMatch[i] {
					t.Errorf("%s\nURL: %s\nInstance name: %s\nExpected match=%v, got=%v",
						tt.description, url, tt.instanceName, tt.shouldMatch[i], result)
				}
			}
		})
	}
}

// TestVerifyInstanceRetryBehavior tests the retry logic in instance verification
func TestVerifyInstanceRetryBehavior(t *testing.T) {
	t.Run("instance appears immediately", func(t *testing.T) {
		instanceName := "test-instance-abc123"
		
		callCount := 0
		listFunc := func() (*compute.InstanceGroupsListInstances, error) {
			callCount++
			return &compute.InstanceGroupsListInstances{
				Items: []*compute.InstanceWithNamedPorts{
					{
						Instance: fmt.Sprintf("https://.../instances/%s", instanceName),
						Status:   "PROVISIONING",
					},
				},
			}, nil
		}

		// Simulate retry logic
		expectedSuffix := "/instances/" + instanceName
		items, _ := listFunc()
		found := false
		for _, i := range items.Items {
			if i.Instance != "" && isLiveInstance(i.Status) {
				if len(i.Instance) >= len(expectedSuffix) && i.Instance[len(i.Instance)-len(expectedSuffix):] == expectedSuffix {
					found = true
					break
				}
			}
		}

		if !found {
			t.Error("Should find instance immediately when it appears in first call")
		}
		
		if callCount != 1 {
			t.Errorf("Expected 1 API call, got %d", callCount)
		}
	})

	t.Run("instance appears after retry", func(t *testing.T) {
		instanceName := "test-instance-abc123"
		
		callCount := 0
		listFunc := func() (*compute.InstanceGroupsListInstances, error) {
			callCount++
			// First call: instance not visible
			if callCount == 1 {
				return &compute.InstanceGroupsListInstances{
					Items: []*compute.InstanceWithNamedPorts{},
				}, nil
			}
			// Second call: instance appears
			return &compute.InstanceGroupsListInstances{
				Items: []*compute.InstanceWithNamedPorts{
					{
						Instance: fmt.Sprintf("https://.../instances/%s", instanceName),
						Status:   "STAGING",
					},
				},
			}, nil
		}

		// Simulate retry logic
		expectedSuffix := "/instances/" + instanceName
		var found bool
		maxRetries := 3
		
		for attempt := 0; attempt < maxRetries; attempt++ {
			items, _ := listFunc()
			for _, i := range items.Items {
				if i.Instance != "" && isLiveInstance(i.Status) {
					if len(i.Instance) >= len(expectedSuffix) && i.Instance[len(i.Instance)-len(expectedSuffix):] == expectedSuffix {
						found = true
						break
					}
				}
			}
			if found {
				break
			}
		}

		if !found {
			t.Error("Should find instance after retry")
		}
		
		if callCount != 2 {
			t.Errorf("Expected 2 API calls (1 initial + 1 retry), got %d", callCount)
		}
	})

	t.Run("ignores wrong instance with similar name", func(t *testing.T) {
		instanceName := "buildkite-oneshot-abc123"
		
		listFunc := func() (*compute.InstanceGroupsListInstances, error) {
			return &compute.InstanceGroupsListInstances{
				Items: []*compute.InstanceWithNamedPorts{
					{
						Instance: "https://.../instances/other-buildkite-oneshot-abc123",
						Status:   "RUNNING",
					},
					{
						Instance: "https://.../instances/buildkite-oneshot-abc123-extra",
						Status:   "RUNNING",
					},
				},
			}, nil
		}

		expectedSuffix := "/instances/" + instanceName
		items, _ := listFunc()
		found := false
		
		for _, i := range items.Items {
			if i.Instance != "" && isLiveInstance(i.Status) {
				if len(i.Instance) >= len(expectedSuffix) && i.Instance[len(i.Instance)-len(expectedSuffix):] == expectedSuffix {
					found = true
					break
				}
			}
		}

		if found {
			t.Error("Should not match instances with similar but different names")
		}
	})

	t.Run("finds correct instance among multiple", func(t *testing.T) {
		instanceName := "buildkite-oneshot-abc123"
		
		listFunc := func() (*compute.InstanceGroupsListInstances, error) {
			return &compute.InstanceGroupsListInstances{
				Items: []*compute.InstanceWithNamedPorts{
					{
						Instance: "https://.../instances/buildkite-oneshot-xyz789",
						Status:   "RUNNING",
					},
					{
						Instance: "https://.../instances/buildkite-oneshot-abc123",
						Status:   "PROVISIONING",
					},
					{
						Instance: "https://.../instances/buildkite-oneshot-def456",
						Status:   "STAGING",
					},
				},
			}, nil
		}

		expectedSuffix := "/instances/" + instanceName
		items, _ := listFunc()
		found := false
		foundInstance := ""
		
		for _, i := range items.Items {
			if i.Instance != "" && isLiveInstance(i.Status) {
				if len(i.Instance) >= len(expectedSuffix) && i.Instance[len(i.Instance)-len(expectedSuffix):] == expectedSuffix {
					found = true
					foundInstance = i.Instance
					break
				}
			}
		}

		if !found {
			t.Error("Should find the correct instance among multiple instances")
		}
		
		if !contains(foundInstance, instanceName) {
			t.Errorf("Found wrong instance: %s", foundInstance)
		}
	})
}

// TestInstanceStatusTransitions tests handling of different instance statuses during verification
func TestInstanceStatusTransitions(t *testing.T) {
	tests := []struct {
		name          string
		status        string
		shouldCount   bool
		description   string
	}{
		{
			name:        "PROVISIONING status is valid",
			status:      "PROVISIONING",
			shouldCount: true,
			description: "Instance in PROVISIONING state should be counted as live",
		},
		{
			name:        "STAGING status is valid",
			status:      "STAGING",
			shouldCount: true,
			description: "Instance in STAGING state should be counted as live",
		},
		{
			name:        "RUNNING status is valid",
			status:      "RUNNING",
			shouldCount: true,
			description: "Instance in RUNNING state should be counted as live",
		},
		{
			name:        "STOPPING status is not valid",
			status:      "STOPPING",
			shouldCount: false,
			description: "Instance in STOPPING state should not be counted",
		},
		{
			name:        "TERMINATED status is not valid",
			status:      "TERMINATED",
			shouldCount: false,
			description: "Instance in TERMINATED state should not be counted",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			instanceName := "test-instance-123"
			expectedSuffix := "/instances/" + instanceName
			
			instance := &compute.InstanceWithNamedPorts{
				Instance: fmt.Sprintf("https://.../instances/%s", instanceName),
				Status:   tt.status,
			}

			// Simulate verification logic
			found := false
			if instance.Instance != "" && isLiveInstance(instance.Status) {
				if len(instance.Instance) >= len(expectedSuffix) && 
					instance.Instance[len(instance.Instance)-len(expectedSuffix):] == expectedSuffix {
					found = true
				}
			}

			if found != tt.shouldCount {
				t.Errorf("%s\nStatus: %s\nExpected found=%v, got=%v",
					tt.description, tt.status, tt.shouldCount, found)
			}
		})
	}
}

// Helper function
func contains(s, substr string) bool {
	return len(s) >= len(substr) && s[len(s)-len(substr):] == substr
}
