package bootstrap

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
)

type InitOptions struct {
	ConfigPath    string
	MCPConfigPath string
	SkillsDir     string
}

type InitReport struct {
	ConfigPath    string
	MCPConfigPath string
	SkillsDir     string
	Created       []string
	Skipped       []string
}

func Init(opts InitOptions) (InitReport, error) {
	report := InitReport{
		ConfigPath:    strings.TrimSpace(opts.ConfigPath),
		MCPConfigPath: strings.TrimSpace(opts.MCPConfigPath),
		SkillsDir:     strings.TrimSpace(opts.SkillsDir),
	}
	if report.ConfigPath == "" {
		report.ConfigPath = "config.json"
	}
	if report.MCPConfigPath == "" {
		report.MCPConfigPath = "mcp.json"
	}
	if report.SkillsDir == "" {
		report.SkillsDir = "skills"
	}

	bundleTarGz, err := decodeInitBundle()
	if err != nil {
		return report, err
	}

	gzr, err := gzip.NewReader(bytes.NewReader(bundleTarGz))
	if err != nil {
		return report, fmt.Errorf("open init bundle: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)

	var configTemplate []byte
	var mcpTemplate []byte

	if err := os.MkdirAll(report.SkillsDir, 0o755); err != nil {
		return report, err
	}

	for {
		hdr, err := tr.Next()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			return report, err
		}

		name := strings.TrimSpace(hdr.Name)
		if name == "" {
			continue
		}
		clean := path.Clean(name)
		if clean == "." || strings.HasPrefix(clean, "../") || clean == ".." || strings.HasPrefix(clean, "/") {
			return report, fmt.Errorf("invalid bundle path: %q", name)
		}

		switch clean {
		case "templates/config.json":
			b, err := io.ReadAll(io.LimitReader(tr, hdr.Size))
			if err != nil {
				return report, err
			}
			configTemplate = b
			continue
		case "templates/mcp.json":
			b, err := io.ReadAll(io.LimitReader(tr, hdr.Size))
			if err != nil {
				return report, err
			}
			mcpTemplate = b
			continue
		}

		if strings.HasPrefix(clean, "skills/") {
			if err := extractSkillEntry(report.SkillsDir, clean, hdr, tr, &report); err != nil {
				return report, err
			}
		}
	}

	if err := writeTemplateFile(report.ConfigPath, 0o600, configTemplate, &report); err != nil {
		return report, err
	}
	if err := writeTemplateFile(report.MCPConfigPath, 0o644, mcpTemplate, &report); err != nil {
		return report, err
	}

	return report, nil
}

func writeTemplateFile(path string, perm os.FileMode, data []byte, report *InitReport) error {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	if _, err := os.Stat(path); err == nil {
		if report != nil {
			report.Skipped = append(report.Skipped, path)
		}
		return nil
	} else if err != nil && !os.IsNotExist(err) {
		return err
	}

	if len(data) == 0 {
		return fmt.Errorf("init bundle missing template for %s", path)
	}

	dir := filepath.Dir(path)
	if dir != "." && dir != "/" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}

	out := data
	if len(out) > 0 && out[len(out)-1] != '\n' {
		out = append(append([]byte(nil), out...), '\n')
	}
	if err := os.WriteFile(path, out, perm); err != nil {
		return err
	}
	if report != nil {
		report.Created = append(report.Created, path)
	}
	return nil
}

func extractSkillEntry(skillsDir string, tarPath string, hdr *tar.Header, r io.Reader, report *InitReport) error {
	rel := strings.TrimPrefix(tarPath, "skills/")
	rel = path.Clean(rel)
	if rel == "." || rel == ".." || strings.HasPrefix(rel, "../") || strings.HasPrefix(rel, "/") {
		return fmt.Errorf("invalid skill entry path: %q", tarPath)
	}

	dest := filepath.Join(skillsDir, filepath.FromSlash(rel))
	if !isWithinDir(skillsDir, dest) {
		return fmt.Errorf("skill entry escapes skills dir: %q", tarPath)
	}

	switch hdr.Typeflag {
	case tar.TypeDir:
		if err := os.MkdirAll(dest, 0o755); err != nil {
			return err
		}
		return nil
	case tar.TypeReg, tar.TypeRegA:
		if _, err := os.Stat(dest); err == nil {
			if report != nil {
				report.Skipped = append(report.Skipped, dest)
			}
			_, _ = io.Copy(io.Discard, io.LimitReader(r, hdr.Size))
			return nil
		} else if err != nil && !os.IsNotExist(err) {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(dest), 0o755); err != nil {
			return err
		}
		f, err := os.OpenFile(dest, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
		if err != nil {
			return err
		}
		_, copyErr := io.CopyN(f, r, hdr.Size)
		closeErr := f.Close()
		if copyErr != nil {
			return copyErr
		}
		if closeErr != nil {
			return closeErr
		}
		if report != nil {
			report.Created = append(report.Created, dest)
		}
		return nil
	default:
		_, _ = io.Copy(io.Discard, io.LimitReader(r, hdr.Size))
		return nil
	}
}

func isWithinDir(root string, target string) bool {
	rootAbs, err := filepath.Abs(root)
	if err != nil {
		return false
	}
	targetAbs, err := filepath.Abs(target)
	if err != nil {
		return false
	}
	rel, err := filepath.Rel(rootAbs, targetAbs)
	if err != nil {
		return false
	}
	rel = filepath.Clean(rel)
	return rel != "." && rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

func decodeInitBundle() ([]byte, error) {
	raw := strings.TrimSpace(initBundleTarGzBase64)
	if raw == "" {
		return nil, errors.New("init bundle is empty (run the build script to regenerate it)")
	}
	decoded, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return nil, fmt.Errorf("decode init bundle: %w", err)
	}
	return decoded, nil
}
