package privacy

import "strings"

// prohibitedDurableFieldAliases mirrors the closed alias list in
// contracts/privacy/data-classes.yaml. It lives in the privacy package so
// every durable writer, including Session 08 structural fingerprints, reuses
// one categorizer instead of maintaining a private copy.
var prohibitedDurableFieldAliases = map[string]struct{}{
	"prompt": {}, "prompt_text": {}, "user_prompt": {}, "response": {},
	"assistant_response": {}, "model_output": {}, "source": {}, "source_code": {},
	"file_content": {}, "tool_input": {}, "tool_output": {}, "tool_result": {},
	"arguments": {}, "command": {}, "command_line": {}, "stdout": {}, "stderr": {},
	"exception": {}, "stack_trace": {}, "path": {}, "file_path": {}, "cwd": {},
	"workspace_roots": {}, "transcript_path": {}, "environment": {}, "env": {},
	"headers": {}, "authorization": {}, "credential": {}, "secret": {},
	"token_value": {}, "api_key": {}, "email": {}, "account_id": {}, "hostname": {},
}

// IsProhibitedDurableField reports whether a field-path segment is forbidden
// at a durable boundary. Matching is case-insensitive and checks every
// dot/bracket-separated segment so "$.safe.prompt" cannot bypass the rule.
func IsProhibitedDurableField(path string) bool {
	normalized := strings.NewReplacer("$", "", "[", ".", "]", ".", "/", ".").Replace(strings.ToLower(path))
	for _, segment := range strings.Split(normalized, ".") {
		segment = strings.TrimSpace(segment)
		if _, prohibited := prohibitedDurableFieldAliases[segment]; prohibited {
			return true
		}
	}
	return false
}
