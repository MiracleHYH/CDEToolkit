// stk-dist is a source-only release helper, never part of the stk package.
package main

import (
	"flag"
	"fmt"
	"os"

	"server-toolkit/internal/distribution"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("用法: stk-dist pack|verify|check-tag [选项]")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	binary := flags.String("binary", "", "built stk executable")
	output := flags.String("out", "dist", "output directory")
	archive := flags.String("archive", "", "release tar.gz")
	platform := flags.String("platform", "", "GOOS/GOARCH")
	versionFile := flags.String("version-file", "VERSION", "source version file")
	tag := flags.String("tag", "", "exact release tag")
	repository := flags.String("repository", "", "require clean tagged Git checkout")
	smoke := flags.Bool("smoke", false, "run native read-only smoke test")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("多余发布参数")
	}
	version, err := distribution.Version(*versionFile)
	if err != nil {
		return err
	}
	switch args[0] {
	case "check-tag":
		if err := distribution.CheckTag(version, *tag); err != nil {
			return err
		}
		if *repository != "" {
			return distribution.CheckRepository(*repository, *tag)
		}
		return nil
	case "pack":
		path, err := distribution.Pack(*binary, *output, version, *platform)
		if err == nil {
			fmt.Println(path)
		}
		return err
	case "verify":
		data, err := distribution.Verify(*archive, version, *platform)
		if err == nil && *smoke {
			err = distribution.Smoke(data, version, *platform)
		}
		if err == nil {
			fmt.Println("release package verified:", *platform, version)
		}
		return err
	default:
		return fmt.Errorf("未知发布操作: %s", args[0])
	}
}
