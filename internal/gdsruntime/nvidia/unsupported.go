//go:build !linux || !amd64 || !cgo

package nvidia

import (
	"fmt"
	"github.com/MiracleHYH/CDEToolkit/internal/gdsapi"
	"github.com/MiracleHYH/CDEToolkit/internal/gdsruntime"
)

func Available() bool { return false }

func (*Backend) Probe(string) (gdsapi.Sample, error) {
	return gdsapi.Sample{}, fmt.Errorf("GDS NVIDIA 后端仅支持 Linux amd64 且启用 cgo 的独立组件")
}

func (*Backend) Allocate(string, uint64, uint64) (gdsruntime.Allocation, error) {
	return nil, fmt.Errorf("GDS NVIDIA 后端仅支持 Linux amd64 且启用 cgo 的独立组件")
}
