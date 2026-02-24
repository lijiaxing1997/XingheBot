package main

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"flag"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

var (
	outPath = flag.String("out", "", "output Go file (default: internal/bootstrap/bundle_data_gen.go)")
	rootDir = flag.String("root", "", "repo root (default: auto-detect via go.mod)")
)

func main() {
	flag.Parse()

	root := strings.TrimSpace(*rootDir)
	if root == "" {
		var err error
		root, err = findGoModRoot()
		if err != nil {
			fatal(err)
		}
	}

	out := strings.TrimSpace(*outPath)
	if out == "" {
		out = filepath.Join(root, "internal", "bootstrap", "bundle_data_gen.go")
	} else if !filepath.IsAbs(out) {
		out = filepath.Join(root, out)
	}

	bundle, err := buildBundle(root)
	if err != nil {
		fatal(err)
	}

	encoded := base64.StdEncoding.EncodeToString(bundle)
	src := renderGo(encoded)

	if err := os.MkdirAll(filepath.Dir(out), 0o755); err != nil {
		fatal(err)
	}
	if err := os.WriteFile(out, []byte(src), 0o644); err != nil {
		fatal(err)
	}
}

func fatal(err error) {
	_, _ = fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}

func findGoModRoot() (string, error) {
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	dir := filepath.Clean(cwd)
	for i := 0; i < 20; i++ {
		if info, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil && info.Mode().IsRegular() {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return "", fmt.Errorf("could not find go.mod from %s", cwd)
}

func buildBundle(root string) ([]byte, error) {
	var buf bytes.Buffer
	gzw, err := gzip.NewWriterLevel(&buf, gzip.BestCompression)
	if err != nil {
		return nil, err
	}
	tw := tar.NewWriter(gzw)

	fixedTime := time.Unix(0, 0).UTC()

	if err := addFile(tw, filepath.Join(root, "config.exm.json"), "templates/config.json", 0o644, fixedTime); err != nil {
		_ = tw.Close()
		_ = gzw.Close()
		return nil, err
	}
	if err := addFile(tw, filepath.Join(root, "slave-config.exm.json"), "templates/slave-config.json", 0o644, fixedTime); err != nil {
		_ = tw.Close()
		_ = gzw.Close()
		return nil, err
	}
	if err := addFile(tw, filepath.Join(root, "mcp.exm.json"), "templates/mcp.json", 0o644, fixedTime); err != nil {
		_ = tw.Close()
		_ = gzw.Close()
		return nil, err
	}

	skills := []string{
		"skill-creator",
		"skill-installer",
		"mcp-builder",
		"mcp-config-manager",
		"ssh-deploy-slave",
	}
	for _, s := range skills {
		src := filepath.Join(root, "skills", s)
		dst := filepath.ToSlash(filepath.Join("skills", s))
		if err := addDir(tw, src, dst, fixedTime); err != nil {
			_ = tw.Close()
			_ = gzw.Close()
			return nil, err
		}
	}

	if err := tw.Close(); err != nil {
		_ = gzw.Close()
		return nil, err
	}
	if err := gzw.Close(); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

func addFile(tw *tar.Writer, srcPath string, tarName string, perm fs.FileMode, fixedTime time.Time) error {
	info, err := os.Stat(srcPath)
	if err != nil {
		return err
	}
	if info.IsDir() {
		return fmt.Errorf("expected file but got dir: %s", srcPath)
	}
	data, err := os.ReadFile(srcPath)
	if err != nil {
		return err
	}
	hdr := &tar.Header{
		Name:       cleanTarName(tarName),
		Mode:       int64(perm.Perm()),
		Size:       int64(len(data)),
		ModTime:    fixedTime,
		AccessTime: fixedTime,
		ChangeTime: fixedTime,
		Typeflag:   tar.TypeReg,
	}
	if err := tw.WriteHeader(hdr); err != nil {
		return err
	}
	_, err = tw.Write(data)
	return err
}

func addDir(tw *tar.Writer, srcDir string, tarPrefix string, fixedTime time.Time) error {
	info, err := os.Stat(srcDir)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("expected dir but got file: %s", srcDir)
	}
	tarPrefix = cleanTarName(tarPrefix)

	// Ensure the root dir entry exists in the archive.
	if err := tw.WriteHeader(&tar.Header{
		Name:       ensureTrailingSlash(tarPrefix),
		Mode:       int64(info.Mode().Perm()),
		ModTime:    fixedTime,
		AccessTime: fixedTime,
		ChangeTime: fixedTime,
		Typeflag:   tar.TypeDir,
	}); err != nil {
		return err
	}

	return walkDirSorted(srcDir, func(path string, d fs.DirEntry) error {
		rel, err := filepath.Rel(srcDir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		if rel == "." {
			return nil
		}
		if shouldSkip(rel, d) {
			if d.IsDir() {
				return filepath.SkipDir
			}
			return nil
		}

		info, err := d.Info()
		if err != nil {
			return err
		}

		name := tarPrefix + "/" + rel
		name = cleanTarName(name)

		if d.IsDir() {
			return tw.WriteHeader(&tar.Header{
				Name:       ensureTrailingSlash(name),
				Mode:       int64(info.Mode().Perm()),
				ModTime:    fixedTime,
				AccessTime: fixedTime,
				ChangeTime: fixedTime,
				Typeflag:   tar.TypeDir,
			})
		}

		if !info.Mode().IsRegular() {
			return nil
		}

		f, err := os.Open(path)
		if err != nil {
			return err
		}
		defer f.Close()

		hdr := &tar.Header{
			Name:       name,
			Mode:       int64(info.Mode().Perm()),
			Size:       info.Size(),
			ModTime:    fixedTime,
			AccessTime: fixedTime,
			ChangeTime: fixedTime,
			Typeflag:   tar.TypeReg,
		}
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		_, err = io.Copy(tw, f)
		return err
	})
}

func walkDirSorted(root string, fn func(path string, d fs.DirEntry) error) error {
	return walkDirSortedInternal(filepath.Clean(root), fn)
}

func walkDirSortedInternal(dir string, fn func(path string, d fs.DirEntry) error) error {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	sort.Slice(entries, func(i, j int) bool {
		return entries[i].Name() < entries[j].Name()
	})
	for _, entry := range entries {
		name := entry.Name()
		if name == "" || strings.HasPrefix(name, ".") {
			continue
		}
		full := filepath.Join(dir, name)
		err := fn(full, entry)
		if err != nil {
			if entry.IsDir() && err == filepath.SkipDir {
				continue
			}
			return err
		}
		if entry.IsDir() {
			if err := walkDirSortedInternal(full, fn); err != nil {
				return err
			}
		}
	}
	return nil
}

func shouldSkip(rel string, d fs.DirEntry) bool {
	base := filepath.Base(rel)
	switch base {
	case ".DS_Store":
		return true
	}
	if d.IsDir() {
		switch base {
		case "node_modules", "__pycache__", "venv", ".git":
			return true
		}
	}
	if strings.HasSuffix(base, ".pyc") {
		return true
	}
	return false
}

func renderGo(encoded string) string {
	const chunkSize = 100
	chunks := make([]string, 0, (len(encoded)+chunkSize-1)/chunkSize)
	for i := 0; i < len(encoded); i += chunkSize {
		end := i + chunkSize
		if end > len(encoded) {
			end = len(encoded)
		}
		chunks = append(chunks, encoded[i:end])
	}

	var b strings.Builder
	b.WriteString("// Code generated by internal/bootstrap/cmd/gen; DO NOT EDIT.\n\n")
	b.WriteString("package bootstrap\n\n")
	b.WriteString("const initBundleTarGzBase64 = \"\" +\n")
	for i, c := range chunks {
		sep := " +\n"
		if i == len(chunks)-1 {
			sep = "\n"
		}
		b.WriteString("\t\"")
		b.WriteString(c)
		b.WriteString("\"")
		b.WriteString(sep)
	}
	return b.String()
}

func cleanTarName(name string) string {
	name = strings.ReplaceAll(name, "\\", "/")
	name = strings.TrimPrefix(name, "/")
	name = filepath.ToSlash(filepath.Clean(name))
	name = strings.TrimPrefix(name, "./")
	name = strings.TrimPrefix(name, "/")
	return name
}

func ensureTrailingSlash(name string) string {
	if strings.HasSuffix(name, "/") {
		return name
	}
	return name + "/"
}
