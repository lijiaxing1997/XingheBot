package skills

import (
    "context"
    "encoding/json"
    "errors"
    "fmt"
    "io"
    "net/http"
    "os"
    "path/filepath"
    "strings"
    "time"

    "test_skill_agent/internal/util"
)

type ghContent struct {
    Type        string `json:"type"`
    Name        string `json:"name"`
    Path        string `json:"path"`
    DownloadURL string `json:"download_url"`
    URL         string `json:"url"`
}

func DownloadGitHubDir(ctx context.Context, repo, path, ref, dest string) error {
    basePath := strings.Trim(path, "/")
    return downloadGitHubPath(ctx, repo, basePath, ref, dest, basePath)
}

func downloadGitHubPath(ctx context.Context, repo, path, ref, dest, basePath string) error {
    contents, err := fetchGitHubContents(ctx, repo, path, ref)
    if err != nil {
        return err
    }
    for _, item := range contents {
        switch item.Type {
        case "file":
            rel := item.Path
            if basePath != "" {
                rel = strings.TrimPrefix(rel, basePath)
                rel = strings.TrimPrefix(rel, "/")
            }
            target := filepath.Join(dest, rel)
            if err := downloadFile(ctx, item.DownloadURL, target); err != nil {
                return err
            }
        case "dir":
            if err := downloadGitHubPath(ctx, repo, item.Path, ref, dest, basePath); err != nil {
                return err
            }
        default:
            continue
        }
    }
    return nil
}

func fetchGitHubContents(ctx context.Context, repo, path, ref string) ([]ghContent, error) {
    if repo == "" {
        return nil, errors.New("repo is required")
    }
    urlPath := strings.Trim(path, "/")
    apiURL := fmt.Sprintf("https://api.github.com/repos/%s/contents/%s", repo, urlPath)
    if urlPath == "" {
        apiURL = fmt.Sprintf("https://api.github.com/repos/%s/contents", repo)
    }
    if ref != "" {
        apiURL += "?ref=" + ref
    }
    req, err := http.NewRequestWithContext(ctx, http.MethodGet, apiURL, nil)
    if err != nil {
        return nil, err
    }
    if token := strings.TrimSpace(os.Getenv("GITHUB_TOKEN")); token != "" {
        req.Header.Set("Authorization", "Bearer "+token)
    } else if token := strings.TrimSpace(os.Getenv("GH_TOKEN")); token != "" {
        req.Header.Set("Authorization", "Bearer "+token)
    }
    req.Header.Set("Accept", "application/vnd.github+json")

    client := &http.Client{Timeout: 60 * time.Second}
    resp, err := client.Do(req)
    if err != nil {
        return nil, err
    }
    defer resp.Body.Close()

    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        data, _ := io.ReadAll(resp.Body)
        return nil, fmt.Errorf("github api error: %s: %s", resp.Status, strings.TrimSpace(string(data)))
    }

    data, err := io.ReadAll(resp.Body)
    if err != nil {
        return nil, err
    }
    trimmed := strings.TrimSpace(string(data))
    if trimmed == "" {
        return nil, errors.New("empty github response")
    }
    if strings.HasPrefix(trimmed, "[") {
        var items []ghContent
        if err := json.Unmarshal(data, &items); err != nil {
            return nil, err
        }
        return items, nil
    }
    var item ghContent
    if err := json.Unmarshal(data, &item); err != nil {
        return nil, err
    }
    return []ghContent{item}, nil
}

func downloadFile(ctx context.Context, url, dest string) error {
    if url == "" {
        return errors.New("download_url is empty")
    }
    req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
    if err != nil {
        return err
    }
    client := &http.Client{Timeout: 60 * time.Second}
    resp, err := client.Do(req)
    if err != nil {
        return err
    }
    defer resp.Body.Close()

    if resp.StatusCode < 200 || resp.StatusCode >= 300 {
        data, _ := io.ReadAll(resp.Body)
        return fmt.Errorf("download error: %s: %s", resp.Status, strings.TrimSpace(string(data)))
    }
    if err := util.EnsureParentDir(dest); err != nil {
        return err
    }
    out, err := os.Create(dest)
    if err != nil {
        return err
    }
    defer out.Close()

    if _, err := io.Copy(out, resp.Body); err != nil {
        return err
    }
    return nil
}
