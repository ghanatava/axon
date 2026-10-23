// Package discovery resolves node processes to Kubernetes workloads without
// relying on executable paths inside a container filesystem.
package discovery

import (
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
)

type Identity struct {
	PID                                   int
	StartTicks, StartStack, Device, Inode uint64
}

func (i Identity) Token() string {
	return fmt.Sprintf("%d:%d:%d:%d", i.StartTicks, i.StartStack, i.Device, i.Inode)
}
func (i Identity) Exe(root string) string { return filepath.Join(root, strconv.Itoa(i.PID), "exe") }
func ParseStat(raw []byte) (start, stack uint64, err error) {
	end := strings.LastIndexByte(string(raw), ')')
	if end < 0 {
		return 0, 0, fmt.Errorf("invalid proc stat")
	}
	fields := strings.Fields(string(raw)[end+1:])
	if len(fields) < 26 {
		return 0, 0, fmt.Errorf("short proc stat")
	}
	start, err = strconv.ParseUint(fields[19], 10, 64)
	if err != nil {
		return
	}
	stack, err = strconv.ParseUint(fields[25], 10, 64)
	return
}
func ReadIdentity(root string, pid int) (Identity, error) {
	i := Identity{PID: pid}
	path := filepath.Join(root, strconv.Itoa(pid))
	raw, err := os.ReadFile(filepath.Join(path, "stat"))
	if err != nil {
		return i, err
	}
	i.StartTicks, i.StartStack, err = ParseStat(raw)
	if err != nil {
		return i, err
	}
	info, err := os.Stat(filepath.Join(path, "exe"))
	if err != nil {
		return i, err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return i, fmt.Errorf("unsupported stat")
	}
	i.Device = uint64(st.Dev)
	i.Inode = st.Ino
	raw, err = os.ReadFile(filepath.Join(path, "stat"))
	if err != nil {
		return i, err
	}
	start, stack, err := ParseStat(raw)
	if err != nil {
		return i, err
	}
	if start != i.StartTicks || stack != i.StartStack {
		return i, fmt.Errorf("process changed during inspection")
	}
	return i, nil
}

type Workload struct {
	Service     string `json:"service"`
	Namespace   string `json:"namespace"`
	Pod         string `json:"pod"`
	PodUID      string `json:"pod_uid"`
	Container   string `json:"container"`
	ContainerID string `json:"container_id"`
	Node        string `json:"node"`
}
type Process struct {
	Identity
	Workload
}

// ContainerIDs handles both cgroupfs and systemd scopes, v1 and v2. Only exact
// runtime IDs are accepted; prefix matching can assign unrelated containers.
func ContainerIDs(raw string) []string {
	fields := strings.FieldsFunc(raw, func(r rune) bool { return !(r >= '0' && r <= '9' || r >= 'a' && r <= 'f') })
	ids := []string{}
	for _, v := range fields {
		if len(v) == 64 {
			ids = append(ids, v)
		}
	}
	return ids
}
func Scan(root string, containers map[string]Workload) ([]Process, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, err
	}
	result := []Process{}
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid <= 0 {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, entry.Name(), "cgroup"))
		if err != nil {
			continue
		}
		var workload Workload
		found := false
		ambiguous := false
		for _, id := range ContainerIDs(string(raw)) {
			if w, ok := containers[id]; ok {
				if found && workload != w {
					ambiguous = true
					break
				}
				workload = w
				found = true
			}
		}
		if !found || ambiguous {
			continue
		}
		identity, err := ReadIdentity(root, pid)
		if err != nil {
			continue
		}
		result = append(result, Process{identity, workload})
	}
	return result, nil
}
