package daemon

const (
	authBearerPrefix = "Bearer "
	contentTypeJSON = "application/json"
	errFmtParseRuntimeMCPConfig = "parse runtime MCP config: %w"
	errMsgProtectedSystemRoot = "path is a protected system root %q"
	errMsgUserHomeDirectory = "path is the user's home directory"
	fileDotMCPJSON = ".mcp.json"
	fileMCPJSON = "mcp.json"
	fileSkillMD = "SKILL.md"
	headerContentType = "Content-Type"
	labelUserConfig = "User config"
	msgGCEligibleForCleanup = "gc: eligible for cleanup"
	promptLocalCodingAgent = "You are running as a local coding agent for a Multica workspace.\n\n"
)
