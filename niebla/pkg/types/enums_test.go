package types

import "testing"

func TestVerdictHelpers(t *testing.T) {
	if !VerdictLGTM.IsApproval() {
		t.Errorf("VerdictLGTM should be an approval")
	}
	if !VerdictApproved.IsApproval() {
		t.Errorf("VerdictApproved should be an approval")
	}
	if VerdictChangesRequested.IsApproval() {
		t.Errorf("VerdictChangesRequested should not be an approval")
	}
	if !VerdictChangesRequested.IsBlocking() {
		t.Errorf("VerdictChangesRequested should be blocking")
	}
	if !VerdictNeedsWork.IsBlocking() {
		t.Errorf("VerdictNeedsWork should be blocking")
	}
	if VerdictLGTM.IsBlocking() {
		t.Errorf("VerdictLGTM should not be blocking")
	}

	for _, v := range AllVerdicts {
		if !v.IsValid() {
			t.Errorf("expected %s to be valid", v)
		}
	}
	if Verdict("random").IsValid() {
		t.Errorf("random verdict should not be valid")
	}
}

func TestCheckRunStatusHelpers(t *testing.T) {
	if !CheckRunStatusPassed.IsSuccess() {
		t.Errorf("CheckRunStatusPassed should be success")
	}
	if CheckRunStatusFailed.IsSuccess() {
		t.Errorf("CheckRunStatusFailed should not be success")
	}
	if !CheckRunStatusPassed.IsTerminal() {
		t.Errorf("CheckRunStatusPassed should be terminal")
	}
	if CheckRunStatusRunning.IsTerminal() {
		t.Errorf("CheckRunStatusRunning should not be terminal")
	}
	if CheckRunStatusCancelled.Canonical() != CheckRunStatusCanceled {
		t.Errorf("expected cancelled to canonicalize to canceled")
	}
}

func TestCLStatusHelpers(t *testing.T) {
	if !CLStatusSubmitted.IsSubmitted() {
		t.Errorf("CLStatusSubmitted should be submitted")
	}
	if CLStatusOpen.IsSubmitted() {
		t.Errorf("CLStatusOpen should not be submitted")
	}
	if !CLStatusOpen.IsOpen() {
		t.Errorf("CLStatusOpen should be open")
	}
	if CLStatusSubmitted.IsOpen() {
		t.Errorf("CLStatusSubmitted should not be open")
	}
}

