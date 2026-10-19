package terminalui

import "regexp"

var outputUserinfo = regexp.MustCompile(`(?i)((?:https?|socks5h?|socks)://)[^/\s<>"']*@`)

// Components already redact their domain-specific secrets. The shared output
// boundary additionally removes terminal controls and URL userinfo so replay
// cannot turn a proxy error into a persisted credential in shell scrollback.
// Prompt values are never captured or replayed.
func safeOutput(text string) string {
	// Match the authority directly so malformed URL escapes in an error do
	// not bypass redaction because a URL parser rejected the trailing path.
	return outputUserinfo.ReplaceAllString(safe(text), "${1}REDACTED@")
}
