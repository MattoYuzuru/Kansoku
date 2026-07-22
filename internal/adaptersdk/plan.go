package adaptersdk

import (
	"errors"

	"kansoku.local/kansoku/internal/installer"
)

// BuildChangePlan turns an internal/installer.Plan into a ChangePlan. This
// is the reuse point TDD 05/ADR 0008 call for: adaptersdk does not invent a
// second apply/rollback/removal mechanism. installerPlan must already have
// been produced by one of installer.BuildCodexPlan/BuildClaudePlan/
// BuildGeminiPlan/BuildCursorPlan or an adapter-specific equivalent built
// the same way, and planSHA256 must be installer.PlanSHA256(installerPlan)
// so a ChangePlan can never silently drift from the installer.Plan it was
// derived from.
//
// Apply/Rollback/Remove for the resulting ChangePlan are performed by
// calling installer.SimulateApply/SimulateRollback/SimulateRemove with the
// same installerPlan and an installer.Approval bound to it -- adaptersdk
// intentionally does not re-implement or wrap those calls, so there is
// exactly one apply/rollback/removal code path in the repository.
func BuildChangePlan(installerPlan installer.Plan, candidateID string, capability CapabilityID) (ChangePlan, error) {
	if candidateID == "" {
		return ChangePlan{}, errors.New("installation_candidate_id_required")
	}
	if !validCapabilityID(capability) {
		return ChangePlan{}, errors.New("unknown_capability_id")
	}
	planSHA256, err := installer.PlanSHA256(installerPlan)
	if err != nil {
		return ChangePlan{}, err
	}
	before := make([]string, 0, len(installerPlan.ExactOperations))
	after := make([]string, 0, len(installerPlan.ExactOperations))
	commands := make([]string, 0, len(installerPlan.ExactOperations))
	for _, operation := range installerPlan.ExactOperations {
		before = append(before, operation.Field+"="+previewString(operation.OldPreview))
		after = append(after, operation.Field+"="+previewString(operation.NewPreview))
		commands = append(commands, operation.Action+" "+operation.Field)
	}
	return ChangePlan{
		PlanID:                  planSHA256,
		InstallationCandidateID: candidateID,
		CapabilityID:            capability,
		PreconditionHash:        installerPlan.OriginalSHA256,
		BeforeSanitizedDiff:     before,
		AfterSanitizedDiff:      after,
		BackupLocator:           installerPlan.BackupLocator,
		Commands:                commands,
		PrivacyDisclosure:       installerPlan.PrivacyTradeoffs,
		RollbackCommand:         installerPlan.RollbackCommand,
	}, nil
}

func previewString(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case bool:
		if typed {
			return "true"
		}
		return "false"
	case nil:
		return "null"
	default:
		return "unsupported_preview_type"
	}
}
