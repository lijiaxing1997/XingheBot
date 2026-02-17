package util

import (
    "errors"
    "io"
    "io/fs"
    "os"
    "path/filepath"
)

func EnsureParentDir(path string) error {
    dir := filepath.Dir(path)
    if dir == "." || dir == "/" {
        return nil
    }
    return os.MkdirAll(dir, 0o755)
}

func CopyFile(src, dst string, overwrite bool) error {
    if !overwrite {
        if _, err := os.Stat(dst); err == nil {
            return errors.New("destination already exists")
        }
    }
    if err := EnsureParentDir(dst); err != nil {
        return err
    }
    in, err := os.Open(src)
    if err != nil {
        return err
    }
    defer in.Close()

    info, err := in.Stat()
    if err != nil {
        return err
    }

    out, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, info.Mode())
    if err != nil {
        return err
    }
    defer out.Close()

    if _, err := io.Copy(out, in); err != nil {
        return err
    }
    return nil
}

func CopyDir(src, dst string, overwrite bool) error {
    info, err := os.Stat(src)
    if err != nil {
        return err
    }
    if !info.IsDir() {
        return errors.New("source is not a directory")
    }
    if _, err := os.Stat(dst); err == nil && !overwrite {
        return errors.New("destination already exists")
    }
    if err := os.MkdirAll(dst, info.Mode()); err != nil {
        return err
    }

    return filepath.WalkDir(src, func(path string, d fs.DirEntry, err error) error {
        if err != nil {
            return err
        }
        if path == src {
            return nil
        }
        rel, err := filepath.Rel(src, path)
        if err != nil {
            return err
        }
        target := filepath.Join(dst, rel)
        if d.IsDir() {
            return os.MkdirAll(target, 0o755)
        }
        return CopyFile(path, target, true)
    })
}

func Move(src, dst string, overwrite bool) error {
    if !overwrite {
        if _, err := os.Stat(dst); err == nil {
            return errors.New("destination already exists")
        }
    }
    if err := EnsureParentDir(dst); err != nil {
        return err
    }
    if err := os.Rename(src, dst); err == nil {
        return nil
    }
    info, err := os.Stat(src)
    if err != nil {
        return err
    }
    if info.IsDir() {
        if err := CopyDir(src, dst, overwrite); err != nil {
            return err
        }
        return os.RemoveAll(src)
    }
    if err := CopyFile(src, dst, overwrite); err != nil {
        return err
    }
    return os.Remove(src)
}
