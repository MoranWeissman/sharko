package observations

import (
	"testing"
	"time"
)

func TestComputeStatus(t *testing.T) {
	now := time.Now()

	tests := []struct {
		name            string
		obs             *Observation
		hasHealthyAddon bool
		wantStatus      ClusterStatus
		wantFailing     bool
		wantErrorCode   string
	}{
		{
			name:       "nil observation returns Unknown",
			obs:        nil,
			wantStatus: StatusUnknown,
		},
		{
			name: "stage1 success returns Connected",
			obs: &Observation{
				LastTestAt:      now,
				LastTestStage:   "stage1",
				LastTestOutcome: "success",
			},
			wantStatus: StatusConnected,
		},
		{
			name: "stage2 success returns Verified",
			obs: &Observation{
				LastTestAt:      now,
				LastTestStage:   "stage2",
				LastTestOutcome: "success",
			},
			wantStatus: StatusVerified,
		},
		{
			name: "test failure returns Unreachable",
			obs: &Observation{
				LastTestAt:        now,
				LastTestStage:     "stage1",
				LastTestOutcome:   "failure",
				LastTestErrorCode: "ERR_NETWORK",
			},
			wantStatus:    StatusUnreachable,
			wantErrorCode: "ERR_NETWORK",
		},
		{
			name:            "healthy addon returns Operational",
			obs:             &Observation{LastTestAt: now, LastTestStage: "stage1", LastTestOutcome: "success"},
			hasHealthyAddon: true,
			wantStatus:      StatusOperational,
		},
		{
			name: "healthy addon + test failure returns Operational with TestFailing",
			obs: &Observation{
				LastTestAt:        now,
				LastTestStage:     "stage1",
				LastTestOutcome:   "failure",
				LastTestErrorCode: "ERR_AUTH",
			},
			hasHealthyAddon: true,
			wantStatus:      StatusOperational,
			wantFailing:     true,
			wantErrorCode:   "ERR_AUTH",
		},
		{
			name: "unknown stage returns Unknown",
			obs: &Observation{
				LastTestAt:      now,
				LastTestStage:   "credentials",
				LastTestOutcome: "success",
			},
			wantStatus: StatusUnknown,
		},
		{
			name: "stage2 failure returns Unreachable",
			obs: &Observation{
				LastTestAt:        now,
				LastTestStage:     "stage2",
				LastTestOutcome:   "failure",
				LastTestErrorCode: "ERR_TIMEOUT",
			},
			wantStatus:    StatusUnreachable,
			wantErrorCode: "ERR_TIMEOUT",
		},
		{
			name:            "healthy addon with nil observation returns Unknown (not Operational)",
			obs:             nil,
			hasHealthyAddon: true,
			wantStatus:      StatusUnknown,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ComputeStatus(tt.obs, tt.hasHealthyAddon)

			if got.Status != tt.wantStatus {
				t.Errorf("Status = %q, want %q", got.Status, tt.wantStatus)
			}
			if got.TestFailing != tt.wantFailing {
				t.Errorf("TestFailing = %v, want %v", got.TestFailing, tt.wantFailing)
			}
			if got.ErrorCode != tt.wantErrorCode {
				t.Errorf("ErrorCode = %q, want %q", got.ErrorCode, tt.wantErrorCode)
			}
		})
	}
}
