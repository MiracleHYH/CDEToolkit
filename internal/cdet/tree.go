package cdet

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
)

// File contents are streamed; plans retain only the one-time tree manifest.
// Copied links must remain inside the copied tree. Absolute internal links
// become relative; all regular file bytes and source files remain unchanged.
type treeEntry struct {
	Path string `json:"path"`
	Fingerprint
	Size    int64     `json:"size,omitempty"`
	ModTime time.Time `json:"-"`
}

type Tree struct {
	Source    string `json:"source,omitempty"`
	Digest    string `json:"digest"`
	RawDigest string `json:"-"`
	Files     int    `json:"files"`
	Bytes     int64  `json:"bytes"`
	entries   []treeEntry
}

func (t *Tree) recomputeDigest() error {
	h := sha256.New()
	encode := json.NewEncoder(h)
	t.Files, t.Bytes = 0, 0
	for _, entry := range t.entries {
		if e := encode.Encode(entry); e != nil {
			return e
		}
		t.Files++
		t.Bytes += entry.Size
	}
	t.Digest = hex.EncodeToString(h.Sum(nil))
	return nil
}

// Always recheck the complete source baseline before copying.
func (t *Tree) sourceSnapshot() (*Tree, error) { return snapshotTree(t.Source) }

// Resolve existing symlink prefixes without cleaning away a/../b first.
// Missing targets are allowed: preserving their link text retains a legitimate
// dangling internal link. A future user change is outside the import snapshot.
func resolveTreeLink(target string, budget int) (string, error) {
	if budget == 0 {
		return "", fmt.Errorf("循环或过深的软链接")
	}
	parts := strings.Split(target, string(os.PathSeparator))
	current := string(os.PathSeparator)
	for i, part := range parts {
		switch part {
		case "", ".":
			continue
		case "..":
			current = filepath.Dir(current)
			continue
		}
		next := filepath.Join(current, part)
		info, e := os.Lstat(next)
		if errors.Is(e, os.ErrNotExist) {
			return filepath.Join(append([]string{next}, parts[i+1:]...)...), nil
		}
		if e != nil {
			return "", e
		}
		if info.Mode()&os.ModeSymlink != 0 {
			link, e := os.Readlink(next)
			if e != nil {
				return "", e
			}
			if !filepath.IsAbs(link) {
				link = current + string(os.PathSeparator) + link
			}
			if i+1 < len(parts) {
				link += string(os.PathSeparator) + strings.Join(parts[i+1:], string(os.PathSeparator))
			}
			return resolveTreeLink(link, budget-1)
		}
		current = next
	}
	return current, nil
}

func snapshotTree(source string) (*Tree, error) {
	if e := noSymlinkParents(filepath.Join(source, "sentinel")); e != nil {
		return nil, e
	}
	t := &Tree{Source: source}
	raw := sha256.New()
	encodeRaw := json.NewEncoder(raw)
	err := filepath.WalkDir(source, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		s, err := d.Info()
		if err != nil {
			return err
		}
		rel, _ := filepath.Rel(source, p)
		fp, err := fingerprint(p)
		if err != nil {
			return err
		}
		entry := treeEntry{Path: rel, Fingerprint: fp, ModTime: s.ModTime()}
		if fp.Kind == "file" {
			entry.Size = s.Size()
		}
		if err = encodeRaw.Encode(entry); err != nil {
			return err
		}
		if fp.Kind == "link" {
			target := fp.Digest
			if !filepath.IsAbs(target) {
				// Joining or cleaning before resolving would change a/../b when
				// a is itself a symlink. Resolve the original link text first.
				target = filepath.Dir(p) + string(os.PathSeparator) + target
			}
			resolved, resolveErr := resolveTreeLink(target, 40)
			if resolveErr != nil {
				return fmt.Errorf("迁入源含无法验证或循环的链接，请先整理源目录: %s", p)
			}
			if !within(source, resolved) || !within(source, filepath.Clean(target)) {
				return fmt.Errorf("迁入源含指向目录外的链接，请先整理源目录: %s", p)
			}
			if filepath.IsAbs(fp.Digest) {
				if filepath.Clean(fp.Digest) != fp.Digest {
					return fmt.Errorf("迁入源的绝对链接含非规范分段，请先整理源目录: %s", p)
				}
				entry.Digest, err = filepath.Rel(filepath.Dir(p), fp.Digest)
				if err != nil {
					return err
				}
			}
		}
		t.entries = append(t.entries, entry)
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(t.entries) == 0 || t.entries[0].Kind != "directory" {
		return nil, fmt.Errorf("迁入源必须是目录")
	}
	t.RawDigest = hex.EncodeToString(raw.Sum(nil))
	if e := t.recomputeDigest(); e != nil {
		return nil, e
	}
	return t, nil
}

func emptyTree() *Tree {
	t := &Tree{entries: []treeEntry{{Path: ".", Fingerprint: Fingerprint{Kind: "directory", Mode: 0700}}}, Files: 1}
	h := sha256.New()
	_ = json.NewEncoder(h).Encode(t.entries[0])
	t.Digest = hex.EncodeToString(h.Sum(nil))
	return t
}

func copyTree(t *Tree, target string) error {
	if !absent(target) {
		return fmt.Errorf("暂存目标已存在")
	}
	if e := noSymlinkParents(filepath.Join(target, "sentinel")); e != nil {
		return e
	}
	for _, entry := range t.entries {
		p := filepath.Join(target, entry.Path)
		if entry.Path != "." && !relative(entry.Path) {
			return fmt.Errorf("目录副本路径越界")
		}
		switch entry.Kind {
		case "directory":
			if e := os.Mkdir(p, 0700); e != nil {
				return e
			}
		case "link":
			if e := os.Symlink(entry.Digest, p); e != nil {
				return e
			}
		case "file":
			source := filepath.Join(t.Source, entry.Path)
			if e := copyFile(source, p, entry.Fingerprint); e != nil {
				return e
			}
			if !entry.ModTime.IsZero() {
				if e := os.Chtimes(p, entry.ModTime, entry.ModTime); e != nil {
					return e
				}
			}
		default:
			return fmt.Errorf("不支持的迁入文件类型")
		}
	}
	// Set directory permissions after copying children, including read-only dirs.
	for i := len(t.entries) - 1; i >= 0; i-- {
		e := t.entries[i]
		if e.Kind == "directory" {
			if er := os.Chmod(filepath.Join(target, e.Path), os.FileMode(e.Mode)); er != nil {
				return er
			}
			if er := syncDirectory(filepath.Join(target, e.Path)); er != nil {
				return er
			}
		}
	}
	copy, e := snapshotTree(target)
	if e != nil {
		return e
	}
	if copy.RawDigest != t.Digest {
		return fmt.Errorf("迁入副本校验失败")
	}
	return syncParent(target)
}

func copyFile(source, target string, want Fingerprint) error {
	if e := noSymlinkParents(source); e != nil {
		return e
	}
	in, e := os.OpenFile(source, os.O_RDONLY|syscall.O_NOFOLLOW, 0)
	if e != nil {
		return e
	}
	defer in.Close()
	s, e := in.Stat()
	if e != nil || !s.Mode().IsRegular() || uint32(s.Mode().Perm()) != want.Mode {
		return fmt.Errorf("复制源发生变化: %s", source)
	}
	out, e := os.OpenFile(target, os.O_WRONLY|os.O_CREATE|os.O_EXCL|syscall.O_NOFOLLOW, 0600)
	if e != nil {
		return e
	}
	h := sha256.New()
	_, e = io.Copy(io.MultiWriter(out, h), in)
	if e == nil && hex.EncodeToString(h.Sum(nil)) != want.Digest {
		e = fmt.Errorf("复制期间源文件发生变化: %s", source)
	}
	if e == nil {
		e = out.Chmod(os.FileMode(want.Mode))
	}
	if e == nil {
		e = out.Sync()
	}
	closeErr := out.Close()
	if e == nil {
		e = closeErr
	}
	return e
}
