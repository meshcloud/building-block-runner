package github

import (
	"fmt"
	"strings"
)

// This file holds the FROZEN, UI-visible message strings (§2.6, byte-identical §7.11). The
// generic-error template embeds the underlying cause text as a placeholder <msg>; only the
// template is frozen (the cause text is inherently implementation-specific).

const (
	failUserMessage         = "Could not trigger the GitHub Action"
	genericErrorPrefix      = "There was an internal error while trying to contact GitHub: "
	pollTimeoutSystemDetail = "Workflow polling timeout after 30 minutes"
	nullWorkflowMessage     = "Workflow file name must not be null"
)

// genericErrorMessage wraps a cause in the §2.6 generic system message.
func genericErrorMessage(cause string) string {
	return genericErrorPrefix + cause
}

// systemMessageForError selects the §2.6 message shape: an *externalCallError (the two
// installation calls) carries the pre-rendered "Request: …\nGitHub responded…" message;
// anything else is the generic-internal-error template.
func systemMessageForError(err error) string {
	if ece, ok := asExternalCallError(err); ok {
		return ece.SystemMessage
	}
	return genericErrorMessage(err.Error())
}

// triggerApiErrorMessage is the trigger-Error result message (§2.6, :138-144).
func triggerApiErrorMessage(statusCode int, body string) string {
	return fmt.Sprintf("GitHub API returned status %d when triggering workflow: %s", statusCode, body)
}

// unsupportedInputsMessage joins the per-input guidance messages by "\n" (§2.6, :125-136).
func unsupportedInputsMessage(workflow string, names []string, omitRunObjectInput bool) string {
	parts := make([]string, 0, len(names))
	for _, n := range names {
		parts = append(parts, unsupportedInputSystemMessage(workflow, n, omitRunObjectInput))
	}
	return strings.Join(parts, "\n")
}

// unsupportedInputSystemMessage is the verbatim per-input guidance (:505-556), incl. the
// YAML snippets and the actions-register-source release link — FROZEN customer UX (§13).
func unsupportedInputSystemMessage(workflow, unsupportedInput string, omitRunObjectInput bool) string {
	switch {
	case unsupportedInput == inputKeyRunUrl && omitRunObjectInput:
		return "Your GitHub workflow '" + workflow + "' does not support the 'buildingBlockRunUrl' input parameter " +
			"but the 'Pass only API URL' option is enabled for this building block definition. " +
			"Please upgrade your workflow to support this input parameter and fetch building block run data from the URL. " +
			"Note: Only the URL is passed, not the full run object. " +
			"See https://github.com/meshcloud/actions-register-source/releases/tag/v2.0.0 for more details."

	case unsupportedInput == inputKeyRunObject && !omitRunObjectInput:
		return "Your GitHub workflow '" + workflow + "' does not support the 'buildingBlockRun' input parameter. " +
			"Please enable the 'Pass only API URL' option in your building block definition to use the modern URL-based approach instead. " +
			"Note: Only the run object is currently passed, not the URL."

	case unsupportedInput == inputKeyApiToken:
		return "Your GitHub workflow '" + workflow + "' does not support the '" + inputKeyApiToken + "' input parameter. " +
			"This input provides an ephemeral API token for accessing the meshStack API. " +
			"Please add this input to your workflow's workflow_dispatch trigger to receive the token, for example:\n" +
			"  on:\n" +
			"    workflow_dispatch:\n" +
			"      inputs:\n" +
			"        " + inputKeyApiToken + ":\n" +
			"          description: 'meshStack API token'\n" +
			"          required: false\n" +
			"          type: string"

	case unsupportedInput == inputKeyRunToken:
		return "Your GitHub workflow '" + workflow + "' does not support the '" + inputKeyRunToken + "' input parameter. " +
			"This input provides an authentication token for updating the building block run status. " +
			"Please add this input to your workflow's workflow_dispatch trigger to receive the token, for example:\n" +
			"  on:\n" +
			"    workflow_dispatch:\n" +
			"      inputs:\n" +
			"        " + inputKeyRunToken + ":\n" +
			"          description: 'Building block run token'\n" +
			"          required: false\n" +
			"          type: string"

	default:
		return "Your GitHub workflow '" + workflow + "' does not support the '" + unsupportedInput + "' input parameter. " +
			"Please update your workflow to accept this input parameter."
	}
}

// triggerSuccessMessages are the IN_PROGRESS trigger-success step messages (§2.6, :558-589):
// user "Triggered GitHub Action '<wf>'. <extra>", system "Triggered action '<wf>'. <extra>".
func triggerSuccessMessages(workflow string, async bool) (user, system string) {
	extra := "Polling for completion status..."
	if async {
		extra = "Will wait for API updates on status..."
	}
	user = fmt.Sprintf("Triggered GitHub Action '%s'. %s", workflow, extra)
	system = fmt.Sprintf("Triggered action '%s'. %s", workflow, extra)
	return user, system
}
