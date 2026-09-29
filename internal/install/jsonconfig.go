package install

// Editing of JSON config files that hold MCP servers (claude_desktop_config.json,
// ~/.claude.json, .mcp.json, …). Every key the installer does not touch is
// kept, in its original order; the file is backed up and replaced atomically.

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// ErrNotJSON means the file exists but is not plain JSON (e.g. it has
// comments). The installer never rewrites such a file.
var ErrNotJSON = errors.New("o arquivo não é JSON puro (tem comentários ou erro de sintaxe); edite-o manualmente")

// object is a JSON object that remembers key order.
type object struct {
	keys []string
	vals map[string]json.RawMessage
}

func parseObject(b []byte) (*object, error) {
	o := &object{vals: map[string]json.RawMessage{}}
	if len(bytes.TrimSpace(b)) == 0 {
		return o, nil
	}
	dec := json.NewDecoder(bytes.NewReader(b))
	tok, err := dec.Token()
	if err != nil {
		return nil, ErrNotJSON
	}
	if d, ok := tok.(json.Delim); !ok || d != '{' {
		return nil, fmt.Errorf("%w (esperava um objeto)", ErrNotJSON)
	}
	for dec.More() {
		kt, err := dec.Token()
		if err != nil {
			return nil, ErrNotJSON
		}
		k, _ := kt.(string)
		var v json.RawMessage
		if err := dec.Decode(&v); err != nil {
			return nil, ErrNotJSON
		}
		o.set(k, v)
	}
	if _, err := dec.Token(); err != nil {
		return nil, ErrNotJSON
	}
	if _, err := dec.Token(); err == nil {
		return nil, fmt.Errorf("%w (conteúdo após o objeto)", ErrNotJSON)
	}
	return o, nil
}

func (o *object) set(k string, v json.RawMessage) {
	if _, ok := o.vals[k]; !ok {
		o.keys = append(o.keys, k)
	}
	o.vals[k] = v
}

func (o *object) del(k string) bool {
	if _, ok := o.vals[k]; !ok {
		return false
	}
	delete(o.vals, k)
	for i, kk := range o.keys {
		if kk == k {
			o.keys = append(o.keys[:i], o.keys[i+1:]...)
			break
		}
	}
	return true
}

func (o *object) marshal() json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, k := range o.keys {
		if i > 0 {
			b.WriteByte(',')
		}
		kb, _ := json.Marshal(k)
		b.Write(kb)
		b.WriteByte(':')
		b.Write(o.vals[k])
	}
	b.WriteByte('}')
	return b.Bytes()
}

// editAt applies fn to the object found by following keys from raw
// (creating missing objects on the way) and returns the new raw document.
func editAt(raw []byte, keys []string, fn func(*object) bool) (json.RawMessage, bool, error) {
	o, err := parseObject(raw)
	if err != nil {
		return nil, false, err
	}
	if len(keys) == 0 {
		changed := fn(o)
		return o.marshal(), changed, nil
	}
	child, ok := o.vals[keys[0]]
	if ok && bytes.Equal(bytes.TrimSpace(child), []byte("null")) {
		child = nil
	}
	nc, changed, err := editAt(child, keys[1:], fn)
	if err != nil {
		return nil, false, err
	}
	if changed {
		o.set(keys[0], nc)
	}
	return o.marshal(), changed, nil
}

// Entry is the part of an MCP server entry the installer reads back.
type Entry struct {
	Type    string            `json:"type,omitempty"`
	Command string            `json:"command"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

func readFile(path string) ([]byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	return b, err
}

// ReadEntries returns the servers found at keys in the file (empty if the
// file does not exist).
func ReadEntries(path string, keys []string) (map[string]Entry, error) {
	b, err := readFile(path)
	if err != nil || b == nil {
		return map[string]Entry{}, err
	}
	o, err := parseObject(b)
	if err != nil {
		return nil, err
	}
	for _, k := range keys {
		v, ok := o.vals[k]
		if !ok {
			return map[string]Entry{}, nil
		}
		if o, err = parseObject(v); err != nil {
			return nil, err
		}
	}
	out := map[string]Entry{}
	for _, k := range o.keys {
		var e Entry
		if json.Unmarshal(o.vals[k], &e) == nil {
			out[k] = e
		}
	}
	return out, nil
}

// WriteResult describes a change to a config file.
type WriteResult struct {
	Changed bool
	Backup  string // path of the backup, if the file existed
}

// UpsertEntry sets servers[name] = entry in the file.
func UpsertEntry(path string, keys []string, name string, entry any, dryRun bool) (WriteResult, error) {
	v, err := json.Marshal(entry)
	if err != nil {
		return WriteResult{}, err
	}
	return modify(path, keys, dryRun, func(o *object) bool {
		if old, ok := o.vals[name]; ok && jsonEqual(old, v) {
			return false
		}
		o.set(name, v)
		return true
	})
}

// RemoveEntry deletes servers[name] from the file.
func RemoveEntry(path string, keys []string, name string, dryRun bool) (WriteResult, error) {
	if !exists(path) {
		return WriteResult{}, nil
	}
	return modify(path, keys, dryRun, func(o *object) bool { return o.del(name) })
}

func modify(path string, keys []string, dryRun bool, fn func(*object) bool) (WriteResult, error) {
	orig, err := readFile(path)
	if err != nil {
		return WriteResult{}, err
	}
	raw, changed, err := editAt(orig, keys, fn)
	if err != nil {
		return WriteResult{}, fmt.Errorf("%s: %w", path, err)
	}
	res := WriteResult{Changed: changed}
	if !changed || dryRun {
		return res, nil
	}
	var out bytes.Buffer
	if err := json.Indent(&out, raw, "", "  "); err != nil {
		return res, err
	}
	out.WriteByte('\n')

	mode := fs.FileMode(0o600)
	if fi, err := os.Stat(path); err == nil {
		mode = fi.Mode().Perm()
	}
	if orig != nil {
		res.Backup = path + ".bak-" + time.Now().Format("20060102-150405")
		if err := os.WriteFile(res.Backup, orig, mode); err != nil {
			return res, fmt.Errorf("backup de %s: %w", path, err)
		}
	}
	return res, atomicWrite(path, out.Bytes(), mode)
}

// atomicWrite writes to a temporary file in the same directory and renames it.
func atomicWrite(path string, data []byte, mode fs.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	f, err := os.CreateTemp(filepath.Dir(path), "."+filepath.Base(path)+".tmp-*")
	if err != nil {
		return err
	}
	tmp := f.Name()
	defer os.Remove(tmp)
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Chmod(tmp, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func jsonEqual(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	xa, _ := json.Marshal(x)
	ya, _ := json.Marshal(y)
	return bytes.Equal(xa, ya)
}

// snippet renders {"<keys...>": {name: entry}} for manual editing.
func snippet(keys []string, name string, entry any) string {
	var v any = map[string]any{name: entry}
	for i := len(keys) - 1; i >= 0; i-- {
		v = map[string]any{keys[i]: v}
	}
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func sortedKeys(m map[string]string) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	sort.Strings(ks)
	return ks
}
