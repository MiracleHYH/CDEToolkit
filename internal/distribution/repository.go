package distribution

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"time"
)

// CheckRepository only reads Git state. It never stages, commits, resets or
// fetches, and is used by release-check rather than ordinary developer builds.
func CheckRepository(root, tag string) error {
	git := func(args ...string) (string, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		base := []string{"-C", root, "-c", "core.fsmonitor=false"}
		out, err := exec.CommandContext(ctx, "git", append(base, args...)...).Output()
		return strings.TrimSpace(string(out)), err
	}
	status, err := git("status", "--porcelain=v1", "--untracked-files=all")
	if err != nil || status != "" {
		return fmt.Errorf("正式发行要求有效且干净的 Git 工作树")
	}
	head, err := git("rev-parse", "--verify", "HEAD^{commit}")
	if err != nil {
		return fmt.Errorf("正式发行要求已有提交")
	}
	target, err := git("rev-parse", "--verify", "refs/tags/"+tag+"^{commit}")
	if err != nil || target != head {
		return fmt.Errorf("发行标签必须已存在并指向当前 HEAD")
	}
	return nil
}
