package appserver

import "github.com/xuzhao123/tern/internal/workspace"

func (c *conn) safeGitReadEnv(dir, index string) ([]string, func(), error) {
	return workspace.SafeGitEnv(c.ctx, dir, dir, index)
}
