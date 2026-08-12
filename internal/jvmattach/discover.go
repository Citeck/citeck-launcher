package jvmattach

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
)

// NSPid returns the pid the process knows itself by — the last entry of the
// NSpid line, i.e. the innermost namespace. For a containerized JVM that is the
// pid inside the container (7, not 3852172), and it is what names the attach
// socket and the trigger file.
//
// A kernel without NSpid (pre-4.1) or a process outside any pid namespace
// yields the host pid, which is the correct answer in both cases.
func (a *Attacher) NSPid(pid int) (int, error) {
	path := filepath.Join(a.procRoot(), strconv.Itoa(pid), "status")
	data, err := os.ReadFile(path) // #nosec G304 -- path is /proc/<int>/status
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", path, err)
	}
	for line := range strings.Lines(string(data)) {
		rest, ok := strings.CutPrefix(line, "NSpid:")
		if !ok {
			continue
		}
		fields := strings.Fields(rest)
		if len(fields) == 0 {
			break
		}
		nsPID, err := strconv.Atoi(fields[len(fields)-1])
		if err != nil {
			return 0, fmt.Errorf("parse NSpid %q: %w", fields[len(fields)-1], err)
		}
		return nsPID, nil
	}
	return pid, nil
}

// IsJVM reports whether libjvm.so is mapped into the process. This is the guard
// on the SIGQUIT in startListener: the signal starts an attach listener in a
// JVM but kills anything else, so "the command line contains java" is not a
// strong enough test — a shell wrapper naming java in its argv would pass it.
//
// An unreadable maps file (a process owned by another uid) is reported as
// not-a-JVM rather than as an error: FindJVM walks a whole subtree and must not
// abort on the first foreign process.
func (a *Attacher) IsJVM(pid int) (bool, error) {
	path := filepath.Join(a.procRoot(), strconv.Itoa(pid), "maps")
	f, err := os.Open(path) // #nosec G304 -- path is /proc/<int>/maps
	if err != nil {
		if os.IsNotExist(err) || os.IsPermission(err) {
			return false, nil
		}
		return false, fmt.Errorf("open %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	// A JVM maps thousands of regions; scan lazily and stop at the first hit.
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for sc.Scan() {
		if strings.Contains(sc.Text(), "/libjvm.so") {
			return true, nil
		}
	}
	if err := sc.Err(); err != nil {
		return false, fmt.Errorf("scan %s: %w", path, err)
	}
	return false, nil
}

// FindJVM returns the pid of the JVM at or below rootPID, searching
// breadth-first up to maxDepth generations.
//
// The container's init process is usually NOT the JVM: the Citeck images run
// `sh -c /entrypoint.sh`, which starts java as a child (a plain `exec java …`
// entrypoint would make init itself the JVM, which is why depth 0 is checked
// first). Ties are broken by lowest pid so the result is deterministic.
func (a *Attacher) FindJVM(rootPID, maxDepth int) (int, error) {
	if isJVM, err := a.IsJVM(rootPID); err != nil {
		return 0, err
	} else if isJVM {
		return rootPID, nil
	}

	children, err := a.childMap()
	if err != nil {
		return 0, err
	}
	frontier := []int{rootPID}
	for depth := 0; depth < maxDepth && len(frontier) > 0; depth++ {
		var next []int
		for _, parent := range frontier {
			kids := slices.Clone(children[parent])
			slices.Sort(kids)
			for _, kid := range kids {
				isJVM, err := a.IsJVM(kid)
				if err != nil {
					return 0, err
				}
				if isJVM {
					return kid, nil
				}
				next = append(next, kid)
			}
		}
		frontier = next
	}
	return 0, fmt.Errorf("%w: below pid %d (depth %d)", ErrNoJVM, rootPID, maxDepth)
}

// childMap builds parent → children from a single procfs scan. Reading
// /proc/<pid>/task/<pid>/children would be cheaper but is optional in the
// kernel (CONFIG_PROC_CHILDREN) and absent on some hosts.
func (a *Attacher) childMap() (map[int][]int, error) {
	entries, err := os.ReadDir(a.procRoot())
	if err != nil {
		return nil, fmt.Errorf("read %s: %w", a.procRoot(), err)
	}
	children := make(map[int][]int)
	for _, e := range entries {
		pid, err := strconv.Atoi(e.Name())
		if err != nil {
			continue // not a process directory
		}
		ppid, err := a.parentOf(pid)
		if err != nil {
			continue // process exited mid-scan, or is not ours to read
		}
		children[ppid] = append(children[ppid], pid)
	}
	return children, nil
}

// parentOf reads PPid from /proc/<pid>/stat.
//
// The comm field is parenthesized and may itself contain spaces and closing
// parens ("(sh -c (x))"), so the fields are counted from the LAST ')' — the
// standard way to parse this file.
func (a *Attacher) parentOf(pid int) (int, error) {
	path := filepath.Join(a.procRoot(), strconv.Itoa(pid), "stat")
	data, err := os.ReadFile(path) // #nosec G304 -- path is /proc/<int>/stat
	if err != nil {
		return 0, fmt.Errorf("read %s: %w", path, err)
	}
	end := strings.LastIndex(string(data), ")")
	if end < 0 {
		return 0, fmt.Errorf("malformed %s", path)
	}
	fields := strings.Fields(string(data)[end+1:])
	// fields[0] is state, fields[1] is ppid.
	if len(fields) < 2 {
		return 0, fmt.Errorf("malformed %s", path)
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		return 0, fmt.Errorf("parse ppid in %s: %w", path, err)
	}
	return ppid, nil
}
