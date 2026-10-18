//go:build !linux && !darwin

package gdsutil

import (
	"fmt"
	"github.com/MiracleHYH/CDEToolkit/internal/gdsapi"
	"syscall"
)

func BootID() (string, error) { return "", fmt.Errorf("当前平台不支持 GDS 进程核验") }
func inspectProcess(int) (gdsapi.Process, error) {
	return gdsapi.Process{}, fmt.Errorf("当前平台不支持 GDS 进程核验")
}
func signalProcess(gdsapi.Process, syscall.Signal) error {
	return fmt.Errorf("当前平台不支持 GDS 进程核验")
}
