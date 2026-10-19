// Package nvidia contains the optional driver-dependent part of GDS. Constructing
// a Backend does not open a driver: only the short-lived probe and worker call it.
package nvidia

type Backend struct {
	nvmlPath  string
	cudaPath  string
	checkHost func() error
}

func New() *Backend { return &Backend{} }
