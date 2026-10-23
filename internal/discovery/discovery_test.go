package discovery

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

func TestCgroupIDs(t *testing.T) {
	id := strings.Repeat("a", 64)
	for _, path := range []string{"0::/kubepods.slice/kubepods-burstable-pod123.slice/cri-containerd-" + id + ".scope\n", "5:cpu,memory:/kubepods/pod123/" + id + "\n", "0::/docker/" + id} {
		ids := ContainerIDs(path)
		if len(ids) != 1 || ids[0] != id {
			t.Fatal(path, ids)
		}
	}
	for _, path := range []string{strings.Repeat("a", 63), strings.Repeat("a", 65), "prefix" + id + "b"} {
		if len(ContainerIDs(path)) != 0 {
			t.Fatal("prefix ID accepted", path)
		}
	}
}
func TestProcessIdentityAndScan(t *testing.T) {
	self, err := ReadIdentity("/proc", os.Getpid())
	if err != nil {
		t.Fatal(err)
	}
	if self.StartTicks == 0 || self.Inode == 0 || self.Token() == "" {
		t.Fatal(self)
	}
	root := t.TempDir()
	dir := filepath.Join(root, "123")
	os.Mkdir(dir, 0700)
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	os.Symlink(executable, filepath.Join(dir, "exe"))
	fields := make([]string, 26)
	for i := range fields {
		fields[i] = "0"
	}
	fields[0] = "S"
	fields[19] = "99"
	fields[25] = "111"
	stat := func() {
		os.WriteFile(filepath.Join(dir, "stat"), []byte("123 (space ) inside) "+strings.Join(fields, " ")), 0600)
	}
	stat()
	id := strings.Repeat("b", 64)
	os.WriteFile(filepath.Join(dir, "cgroup"), []byte("0::/cri-containerd-"+id+".scope"), 0600)
	w := Workload{Pod: "right", Namespace: "demo"}
	processes, err := Scan(root, map[string]Workload{id: w})
	if err != nil || len(processes) != 1 || processes[0].Workload != w {
		t.Fatal(processes, err)
	}
	before := processes[0].Identity
	fields[25] = "222"
	stat()
	after, err := ReadIdentity(root, 123)
	if err != nil || before.Token() == after.Token() {
		t.Fatal("same-PID exec not detected", before, after, err)
	}
	fields[19] = "100"
	stat()
	reuse, _ := ReadIdentity(root, 123)
	if reuse.Token() == after.Token() {
		t.Fatal("PID reuse not detected")
	}
	processes, err = Scan(root, map[string]Workload{strings.Repeat("b", 63): w})
	if err != nil || len(processes) != 0 {
		t.Fatal("partial cgroup match", processes, err)
	}
	if _, _, err := ParseStat([]byte("bad")); err == nil {
		t.Fatal("bad stat accepted")
	}
}
func TestKubernetesPaginationFilteringAndTokenRotation(t *testing.T) {
	token := filepath.Join(t.TempDir(), "token")
	os.WriteFile(token, []byte("one\n"), 0600)
	calls := 0
	expectedToken := "one"
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer "+expectedToken {
			t.Error("token rotation not honored")
		}
		if r.URL.Query().Get("fieldSelector") != "spec.nodeName=node-a" || r.URL.Query().Get("labelSelector") != "trace=yes" {
			t.Error("missing selectors", r.URL)
		}
		page := 1
		if r.URL.Query().Get("continue") == "next" {
			page = 2
		}
		items := []any{}
		pod := func(name, ns, node string) any {
			return map[string]any{"metadata": map[string]any{"name": name, "namespace": ns, "uid": "uid-" + name, "labels": map[string]string{"app.kubernetes.io/name": "echo"}}, "spec": map[string]string{"nodeName": node}, "status": map[string]any{"containerStatuses": []any{map[string]any{"name": "app", "containerID": "containerd://" + strings.Repeat(strconv.Itoa(page), 64), "state": map[string]any{"running": map[string]any{}}}}}}
		}
		metadata := map[string]string{}
		if page == 1 {
			items = append(items, pod("ignored", "blocked", "node-a"), pod("other-node", "demo", "node-b"))
			metadata["continue"] = "next"
		} else {
			items = append(items, pod("right", "demo", "node-a"))
		}
		json.NewEncoder(w).Encode(map[string]any{"metadata": metadata, "items": items})
	}))
	defer server.Close()
	client := &Client{URL: server.URL, Node: "node-a", Selector: "trace=yes", TokenFile: token, Excluded: map[string]bool{"blocked": true}, HTTP: server.Client()}
	for _, value := range []string{"one", "two"} {
		expectedToken = value
		os.WriteFile(token, []byte(value), 0600)
		containers, err := client.Containers(context.Background())
		if err != nil || len(containers) != 1 {
			t.Fatal(containers, err)
		}
		w := containers[strings.Repeat("2", 64)]
		if w.Pod != "right" || w.Service != "echo" || w.Node != "node-a" {
			t.Fatal(w)
		}
	}
	if calls != 4 {
		t.Fatal(calls)
	}
}
func TestKubernetesFailsClosed(t *testing.T) {
	token := filepath.Join(t.TempDir(), "token")
	os.WriteFile(token, []byte("token"), 0600)
	for _, body := range []string{"forbidden", "bad-json", "repeat"} {
		t.Run(body, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch body {
				case "forbidden":
					w.WriteHeader(403)
				case "bad-json":
					fmt.Fprint(w, "{")
				case "repeat":
					fmt.Fprint(w, `{"metadata":{"continue":"again"},"items":[]}`)
				}
			}))
			defer server.Close()
			c := &Client{URL: server.URL, Node: "n", TokenFile: token, HTTP: server.Client()}
			if got, err := c.Containers(context.Background()); err == nil || got != nil {
				t.Fatal("untrusted partial metadata accepted", got, err)
			}
		})
	}
}

func TestDeletedExecutable(t *testing.T) {
	if os.Getenv("AXON_DISCOVERY_TEST_HELPER") == "1" {
		io.Copy(io.Discard, os.Stdin)
		os.Exit(0)
	}
	source, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "deleted-process")
	if err := os.WriteFile(path, raw, 0700); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(path, "-test.run=^TestDeletedExecutable$")
	cmd.Env = append(os.Environ(), "AXON_DISCOVERY_TEST_HELPER=1")
	input, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() { cmd.Process.Kill(); cmd.Wait() }()
	var before Identity
	for n := 0; n < 100; n++ {
		before, err = ReadIdentity("/proc", cmd.Process.Pid)
		if err == nil {
			break
		}
		time.Sleep(time.Millisecond)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	after, err := ReadIdentity("/proc", cmd.Process.Pid)
	if err != nil || before != after {
		t.Fatal(before, after, err)
	}
	exe, err := os.Open(after.Exe("/proc"))
	if err != nil {
		t.Fatal("deleted image unavailable", err)
	}
	defer exe.Close()
	var magic [4]byte
	if _, err := io.ReadFull(exe, magic[:]); err != nil || string(magic[:]) != "\x7fELF" {
		t.Fatal("deleted image not readable", magic, err)
	}
}
