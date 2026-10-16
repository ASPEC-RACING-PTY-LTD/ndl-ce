// Package storscan measures what uses space under a directory, like a
// tree-size tool, and deletes chosen items safely. It runs in the agent as
// root. Trees are pruned while walking so memory stays bounded on large disks.
package storscan

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/google/uuid"
)

// keepChildren is how many children each folder keeps; the rest roll up.
const keepChildren = 200

// Node is one file or folder. Size is space allocated on disk (what frees
// up when it is deleted); Apparent is the logical size, larger for sparse
// files such as container disk images.
type Node struct {
	Name     string `json:"name"`
	Dir      bool   `json:"dir"`
	Symlink  bool   `json:"symlink,omitempty"`
	Size     int64  `json:"size"`
	Apparent int64  `json:"apparent"`
	Files    int64  `json:"files"`
	Dirs     int64  `json:"dirs"`
	MTime    int64  `json:"mtime"`
	Mount    bool   `json:"mount,omitempty"`
	// HasChildren is set on shallow copies whose children were left out.
	HasChildren bool    `json:"has_children,omitempty"`
	Err         string  `json:"error,omitempty"`
	Other       *Rollup `json:"other,omitempty"`
	Children    []*Node `json:"children,omitempty"`
}

// Rollup counts the children too small to keep individually.
type Rollup struct {
	Count int64 `json:"count"`
	Size  int64 `json:"size"`
}

// Status is a snapshot of a scan's progress.
type Status struct {
	ID       string    `json:"id"`
	Root     string    `json:"root"`
	OneFS    bool      `json:"one_filesystem"`
	State    string    `json:"state"`
	Error    string    `json:"error,omitempty"`
	Started  time.Time `json:"started_at"`
	Finished time.Time `json:"finished_at,omitempty"`
	Entries  int64     `json:"entries"`
	Bytes    int64     `json:"bytes"`
	Current  string    `json:"current,omitempty"`
	FSTotal  int64     `json:"fs_total"`
	FSFree   int64     `json:"fs_free"`
}

// Scan is one measurement of a root.
type Scan struct {
	ID       string    `json:"id"`
	Root     string    `json:"root"`
	OneFS    bool      `json:"one_filesystem"`
	State    string    `json:"state"`
	Error    string    `json:"error,omitempty"`
	Started  time.Time `json:"started_at"`
	Finished time.Time `json:"finished_at,omitempty"`
	Entries  int64     `json:"entries"`
	Bytes    int64     `json:"bytes"`
	Current  string    `json:"current,omitempty"`
	// FSTotal and FSFree describe the filesystem holding the root.
	FSTotal int64 `json:"fs_total"`
	FSFree  int64 `json:"fs_free"`

	tree    *Node
	cancel  context.CancelFunc
	entries atomic.Int64
	bytes   atomic.Int64
	current atomic.Value
}

// Scan states.
const (
	StateRunning   = "running"
	StateDone      = "done"
	StateFailed    = "failed"
	StateCancelled = "cancelled"
)

// Manager runs scans. One scan runs at a time so disks are not thrashed.
type Manager struct {
	mu    sync.Mutex
	scans []*Scan
	// Mounts returns mount points (tests replace it).
	Mounts func() ([]string, error)
}

// Start begins scanning root in the background.
func (m *Manager) Start(root string, oneFS bool) (*Scan, error) {
	root = filepath.Clean(root)
	if !filepath.IsAbs(root) {
		return nil, fmt.Errorf("scan root must be an absolute path")
	}
	st, err := os.Lstat(root)
	if err != nil || !st.IsDir() {
		return nil, fmt.Errorf("%s is not a folder", root)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.scans {
		if s.State == StateRunning {
			return nil, fmt.Errorf("a scan of %s is already running; wait for it or cancel it", s.Root)
		}
	}
	ctx, cancel := context.WithCancel(context.Background())
	s := &Scan{ID: uuid.NewString(), Root: root, OneFS: oneFS, State: StateRunning, Started: time.Now().UTC(), cancel: cancel}
	var sfs syscall.Statfs_t
	if syscall.Statfs(root, &sfs) == nil {
		s.FSTotal = int64(sfs.Blocks) * int64(sfs.Bsize)
		s.FSFree = int64(sfs.Bavail) * int64(sfs.Bsize)
	}
	m.scans = append(m.scans, s)
	if len(m.scans) > 4 {
		m.scans = m.scans[len(m.scans)-4:]
	}
	go func() {
		defer cancel()
		tree, err := s.walk(ctx)
		m.mu.Lock()
		defer m.mu.Unlock()
		s.tree = tree
		s.Finished = time.Now().UTC()
		switch {
		case ctx.Err() != nil:
			s.State = StateCancelled
		case err != nil:
			s.State, s.Error = StateFailed, err.Error()
		default:
			s.State = StateDone
		}
	}()
	return s, nil
}

// Get returns a snapshot of a scan's progress.
func (m *Manager) Get(id string) (Status, *Node, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.scans {
		if s.ID == id {
			out := Status{ID: s.ID, Root: s.Root, OneFS: s.OneFS, State: s.State, Error: s.Error, Started: s.Started, Finished: s.Finished,
				Entries: s.entries.Load(), Bytes: s.bytes.Load(), FSTotal: s.FSTotal, FSFree: s.FSFree}
			if c, ok := s.current.Load().(string); ok && s.State == StateRunning {
				out.Current = c
			}
			return out, s.tree, true
		}
	}
	return Status{}, nil, false
}

// Latest returns the most recent scan of root, if any.
func (m *Manager) Latest(root string) (string, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := len(m.scans) - 1; i >= 0; i-- {
		if m.scans[i].Root == filepath.Clean(root) {
			return m.scans[i].ID, true
		}
	}
	return "", false
}

// Cancel stops a running scan.
func (m *Manager) Cancel(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, s := range m.scans {
		if s.ID == id && s.State == StateRunning {
			s.cancel()
			return true
		}
	}
	return false
}

type inodeKey struct{ dev, ino uint64 }

func (s *Scan) walk(ctx context.Context) (*Node, error) {
	rootSt, err := os.Lstat(s.Root)
	if err != nil {
		return nil, err
	}
	rootDev := statOf(rootSt).Dev
	seen := map[inodeKey]bool{}
	var visit func(path, name string, dev uint64) *Node
	visit = func(path, name string, dev uint64) *Node {
		n := &Node{Name: name, Dir: true}
		if ctx.Err() != nil {
			return n
		}
		s.current.Store(path)
		ents, err := os.ReadDir(path)
		if err != nil {
			n.Err = compactErr(err)
		}
		var kids []*Node
		for _, e := range ents {
			if ctx.Err() != nil {
				break
			}
			child := filepath.Join(path, e.Name())
			info, err := e.Info()
			if err != nil {
				kids = append(kids, &Node{Name: e.Name(), Err: compactErr(err)})
				continue
			}
			st := statOf(info)
			s.entries.Add(1)
			switch {
			case info.Mode()&os.ModeSymlink != 0:
				kids = append(kids, &Node{Name: e.Name(), Symlink: true, MTime: info.ModTime().Unix()})
				n.Files++
			case info.IsDir():
				if s.OneFS && uint64(st.Dev) != dev {
					kids = append(kids, &Node{Name: e.Name(), Dir: true, Mount: true, MTime: info.ModTime().Unix()})
					continue
				}
				c := visit(child, e.Name(), uint64(st.Dev))
				c.MTime = info.ModTime().Unix()
				c.Size += st.Blocks * 512
				n.Dirs += c.Dirs + 1
				n.Files += c.Files
				n.Size += c.Size
				n.Apparent += c.Apparent
				kids = append(kids, c)
			default:
				alloc := st.Blocks * 512
				if st.Nlink > 1 {
					k := inodeKey{uint64(st.Dev), st.Ino}
					if seen[k] {
						alloc = 0
					}
					seen[k] = true
				}
				f := &Node{Name: e.Name(), Size: alloc, Apparent: info.Size(), Files: 1, MTime: info.ModTime().Unix()}
				s.bytes.Add(alloc)
				n.Files++
				n.Size += alloc
				n.Apparent += info.Size()
				kids = append(kids, f)
			}
		}
		n.Children, n.Other = prune(kids)
		return n
	}
	root := visit(s.Root, filepath.Base(s.Root), uint64(rootDev))
	root.Name = s.Root
	root.Size += statOf(rootSt).Blocks * 512
	root.MTime = rootSt.ModTime().Unix()
	return root, ctx.Err()
}

func statOf(info os.FileInfo) *syscall.Stat_t {
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		return st
	}
	return &syscall.Stat_t{}
}

// prune keeps the largest children and rolls up the rest.
func prune(kids []*Node) ([]*Node, *Rollup) {
	sort.Slice(kids, func(i, j int) bool {
		if kids[i].Size != kids[j].Size {
			return kids[i].Size > kids[j].Size
		}
		return kids[i].Name < kids[j].Name
	})
	if len(kids) <= keepChildren {
		return kids, nil
	}
	r := &Rollup{}
	for _, k := range kids[keepChildren:] {
		r.Count++
		r.Size += k.Size
	}
	return kids[:keepChildren], r
}

func compactErr(err error) string {
	var pe *os.PathError
	if errors.As(err, &pe) {
		return pe.Err.Error()
	}
	return err.Error()
}

// Find returns the node at rel (a slash path below the root).
func Find(tree *Node, rel string) (*Node, error) {
	if tree == nil {
		return nil, fmt.Errorf("the scan has no results yet")
	}
	rel = strings.Trim(filepath.ToSlash(filepath.Clean("/"+rel)), "/")
	n := tree
	if rel == "" {
		return n, nil
	}
	for _, part := range strings.Split(rel, "/") {
		var next *Node
		for _, c := range n.Children {
			if c.Name == part {
				next = c
			}
		}
		if next == nil {
			return nil, fmt.Errorf("%s is not in the scan results (it may be among the smaller items)", rel)
		}
		n = next
	}
	return n, nil
}

// Shallow copies a node with its direct children but not theirs.
func Shallow(n *Node) Node {
	out := *n
	out.Children = make([]*Node, 0, len(n.Children))
	for _, c := range n.Children {
		cc := *c
		cc.Children = nil
		cc.HasChildren = len(c.Children) > 0 || c.Other != nil
		out.Children = append(out.Children, &cc)
	}
	return out
}
