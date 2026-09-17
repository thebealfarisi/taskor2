package repocache

const (
	envGitConfigCountPrefix = "GIT_CONFIG_COUNT="
	errFmtGitCommandTimeout = "git command timed out after %s: %w"
	gitFlagForce = "--force"
	gitFlagGitCommonDir = "--git-common-dir"
	gitFlagNoTags = "--no-tags"
	gitFlagVerify = "--verify"
	gitSubcommandRevParse = "rev-parse"
	msgRepoCheckoutInstallHookFailed = "repo checkout: install co-authored-by hook failed (non-fatal)"
	msgRepoCheckoutRemoveHookFailed = "repo checkout: remove co-authored-by hook failed (non-fatal)"
	refPrefixHeads = "refs/heads/"
	refPrefixRemotesOrigin = "refs/remotes/origin/"
)
