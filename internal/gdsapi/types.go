// Package gdsapi defines the small, versioned contract shared by Nido and GDS.
// It contains no native driver dependencies or process side effects.
package gdsapi

const ProtocolVersion = 1

type Process struct {
	PID               int    `json:"pid"`
	UID               uint32 `json:"uid"`
	Boot              string `json:"boot"`
	Started           string `json:"started"`
	Executable        string `json:"executable"`
	ExecutableDeleted bool   `json:"executable_deleted,omitempty"`
}

type Record struct {
	Schema      int     `json:"schema"`
	Token       string  `json:"token"`
	ParentToken string  `json:"parent_token,omitempty"`
	Process     Process `json:"process"`
	ConfigHash  string  `json:"config_hash"`
	ProgramHash string  `json:"program_hash"`
	UUID        string  `json:"uuid"`
	Phase       string  `json:"phase"`
}

type Sample struct {
	UUID         string `json:"uuid"`
	TotalBytes   uint64 `json:"total_bytes"`
	FreeBytes    uint64 `json:"free_bytes"`
	Utilization  int    `json:"utilization"`
	ComputePIDs  []int  `json:"compute_pids"`
	GraphicsPIDs []int  `json:"graphics_pids"`
	Complete     bool   `json:"complete"`
	Supported    bool   `json:"supported"`
	Problem      string `json:"problem,omitempty"`
}

type State struct {
	Schema         int    `json:"schema"`
	Boot           string `json:"boot_id"`
	Token          string `json:"token"`
	UUID           string `json:"uuid"`
	Runtime        string `json:"runtime"`
	Scheduler      string `json:"scheduler"`
	Timestamp      int64  `json:"timestamp"`
	ObservedBootNS int64  `json:"observed_boot_ns"`
	IdleSeconds    int64  `json:"idle_seconds"`
	WorkerPID      int    `json:"worker_pid"`
	AllocatedBytes uint64 `json:"allocated_bytes"`
	ConfigHash     string `json:"config_hash"`
	Problem        string `json:"problem,omitempty"`
}

// ExpiresAtBootNS is an absolute CLOCK_BOOTTIME deadline, never a receipt TTL.
type Lease struct {
	Type            string `json:"type"`
	Protocol        int    `json:"protocol"`
	Boot            string `json:"boot_id"`
	Token           string `json:"token"`
	UUID            string `json:"uuid"`
	Seq             uint64 `json:"seq"`
	ExpiresAtBootNS int64  `json:"expires_at_boottime_ns"`
	Bytes           uint64 `json:"bytes,omitempty"`
	MinFreeBytes    uint64 `json:"min_free_bytes,omitempty"`
}

type Ready struct {
	Type           string `json:"type"`
	Protocol       int    `json:"protocol"`
	Token          string `json:"token"`
	UUID           string `json:"uuid"`
	PID            int    `json:"pid"`
	Seq            uint64 `json:"seq"`
	AllocatedBytes uint64 `json:"allocated_bytes"`
	TotalBytes     uint64 `json:"total_bytes"`
	Problem        string `json:"problem,omitempty"`
}

type Metadata struct {
	Product  string `json:"product"`
	Version  string `json:"version"`
	Protocol int    `json:"protocol"`
	Platform string `json:"platform"`
	Native   bool   `json:"native"`
}
