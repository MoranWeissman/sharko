package observations

// ComputeStatus is a pure function that derives a cluster's status from its
// last observation and whether it has at least one healthy addon in ArgoCD.
func ComputeStatus(obs *Observation, hasHealthyAddon bool) StatusResult {
	if obs == nil {
		return StatusResult{Status: StatusUnknown}
	}

	if hasHealthyAddon {
		r := StatusResult{
			Status:     StatusOperational,
			LastTestAt: obs.LastTestAt,
		}
		if obs.LastTestOutcome == "failure" {
			r.TestFailing = true
			r.ErrorCode = obs.LastTestErrorCode
		}
		return r
	}

	if obs.LastTestOutcome == "failure" {
		return StatusResult{
			Status:     StatusUnreachable,
			LastTestAt: obs.LastTestAt,
			ErrorCode:  obs.LastTestErrorCode,
		}
	}

	switch obs.LastTestStage {
	case "stage2":
		return StatusResult{Status: StatusVerified, LastTestAt: obs.LastTestAt}
	case "stage1":
		return StatusResult{Status: StatusConnected, LastTestAt: obs.LastTestAt}
	default:
		return StatusResult{Status: StatusUnknown, LastTestAt: obs.LastTestAt}
	}
}
