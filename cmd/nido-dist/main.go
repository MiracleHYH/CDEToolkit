// nido-dist is a source-only release helper, never part of the nido package.
package main

import (
	"flag"
	"fmt"
	"os"
	"strings"

	"github.com/MiracleHYH/CDEToolkit/internal/distribution"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

func run(args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("用法: nido-dist pack|verify|installer|check-tag|gds-pack|gds-verify|gds-check-tag [选项]")
	}
	flags := flag.NewFlagSet(args[0], flag.ContinueOnError)
	installerSource := flags.String("source", "scripts/install.sh", "POSIX CLI installer source")
	verifyInstaller := flags.Bool("verify", false, "verify installer matches source")
	binary := flags.String("binary", "", "built nido executable")
	output := flags.String("out", "dist", "output directory")
	archive := flags.String("archive", "", "release tar.gz")
	platform := flags.String("platform", "", "GOOS/GOARCH")
	defaultVersionFile := "VERSION"
	if strings.HasPrefix(args[0], "gds-") {
		defaultVersionFile = "GDS_VERSION"
	}
	versionFile := flags.String("version-file", defaultVersionFile, "source version file")
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
	case "gds-check-tag":
		if err := distribution.CheckGDSTag(version, *tag); err != nil {
			return err
		}
		if *repository != "" {
			return distribution.CheckRepository(*repository, *tag)
		}
		return nil
	case "gds-pack":
		path, err := distribution.PackGDS(*binary, *output, version)
		if err == nil {
			fmt.Println(path)
		}
		return err
	case "gds-verify":
		data, err := distribution.VerifyGDS(*binary, version)
		if err == nil && *smoke {
			err = distribution.SmokeGDS(data, version)
		}
		if err == nil {
			fmt.Println("GDS release verified: linux/amd64", version)
		}
		return err
	case "installer":
		return distribution.Installer(*installerSource, *output, *verifyInstaller)
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
