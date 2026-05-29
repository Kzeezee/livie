package runner

import (
	"archive/zip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"

	tea "charm.land/bubbletea/v2"
)

const githubLatestRelease = "https://api.github.com/repos/ggerganov/llama.cpp/releases/latest"

// ProgressUpdate is emitted by the download goroutine on each chunk received
// and once more when extraction completes (Done=true).
type ProgressUpdate struct {
	Downloaded int64  // bytes received so far
	Total      int64  // total size; 0 = unknown (no Content-Length)
	Done       bool   // true on the final update
	Err        error  // non-nil only on the final update when something went wrong
	BinaryPath string // populated on successful completion (Done && Err == nil)
}

// StartDownload launches the download goroutine and returns a channel of
// ProgressUpdate values. Call DownloadProgressCmd to drain the channel in a
// Bubbletea-friendly way.
//
// ctx can be cancelled to abort the download; cancellation is reported as an
// error on the final ProgressUpdate.
func StartDownload(ctx context.Context, platform Platform, destDir string) <-chan ProgressUpdate {
	ch := make(chan ProgressUpdate, 4)
	go func() {
		defer close(ch)
		if err := downloadAndExtract(ctx, platform, destDir, ch); err != nil {
			ch <- ProgressUpdate{Done: true, Err: err}
		}
	}()
	return ch
}

// DownloadProgressCmd returns a tea.Cmd that blocks until the next
// ProgressUpdate is available on ch, then wraps it as a DownloadProgressMsg.
// Re-issue this cmd for every non-Done update.
func DownloadProgressCmd(ch <-chan ProgressUpdate) tea.Cmd {
	return func() tea.Msg {
		return DownloadProgressMsg(<-ch)
	}
}

// downloadAndExtract performs the full download + extraction pipeline,
// writing progress updates to ch.
func downloadAndExtract(ctx context.Context, platform Platform, destDir string, ch chan<- ProgressUpdate) error {
	// 1. Resolve the download URL from the GitHub API.
	url, size, err := resolveAssetURL(ctx, platform)
	if err != nil {
		return fmt.Errorf("resolve release asset: %w", err)
	}

	// 2. Download the ZIP to a temp file with streaming progress.
	tmpFile, err := os.CreateTemp("", "livie-llama-*.zip")
	if err != nil {
		return fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if err := downloadToFile(ctx, url, size, tmpFile, ch); err != nil {
		tmpFile.Close()
		return err
	}
	tmpFile.Close()

	// 3. Extract the binary.
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return fmt.Errorf("mkdir destDir: %w", err)
	}

	binaryPath, err := extractBinary(tmpPath, platform, destDir)
	if err != nil {
		return fmt.Errorf("extract binary: %w", err)
	}

	ch <- ProgressUpdate{
		Downloaded: size,
		Total:      size,
		Done:       true,
		BinaryPath: binaryPath,
	}
	return nil
}

// resolveAssetURL calls the GitHub Releases API and returns the download URL
// and byte size of the ZIP matching platform.ReleaseAssetSuffix().
func resolveAssetURL(ctx context.Context, platform Platform) (url string, size int64, err error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, githubLatestRelease, nil)
	if err != nil {
		return "", 0, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", 0, fmt.Errorf("github API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", 0, fmt.Errorf("github API returned %s", resp.Status)
	}

	var release struct {
		Assets []struct {
			Name               string `json:"name"`
			BrowserDownloadURL string `json:"browser_download_url"`
			Size               int64  `json:"size"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&release); err != nil {
		return "", 0, fmt.Errorf("parse release JSON: %w", err)
	}

	suffix := platform.ReleaseAssetSuffix()
	for _, a := range release.Assets {
		if len(a.Name) >= len(suffix) && a.Name[len(a.Name)-len(suffix):] == suffix {
			return a.BrowserDownloadURL, a.Size, nil
		}
	}
	return "", 0, fmt.Errorf("no release asset matching suffix %q", suffix)
}

// downloadToFile streams url into f, emitting ProgressUpdate values to ch.
func downloadToFile(ctx context.Context, url string, totalSize int64, f *os.File, ch chan<- ProgressUpdate) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("download: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("download returned %s", resp.Status)
	}

	if resp.ContentLength > 0 && totalSize == 0 {
		totalSize = resp.ContentLength
	}

	buf := make([]byte, 32*1024)
	var downloaded int64
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		n, readErr := resp.Body.Read(buf)
		if n > 0 {
			if _, err := f.Write(buf[:n]); err != nil {
				return fmt.Errorf("write temp file: %w", err)
			}
			downloaded += int64(n)
			// Non-blocking send; drop update if consumer is slow.
			select {
			case ch <- ProgressUpdate{Downloaded: downloaded, Total: totalSize}:
			default:
			}
		}
		if readErr == io.EOF {
			break
		}
		if readErr != nil {
			return fmt.Errorf("read response: %w", readErr)
		}
	}
	return nil
}

// extractBinary opens the ZIP at zipPath, finds the llama-server binary, and
// extracts it to destDir with executable permissions.
func extractBinary(zipPath string, platform Platform, destDir string) (string, error) {
	zr, err := zip.OpenReader(zipPath)
	if err != nil {
		return "", fmt.Errorf("open zip: %w", err)
	}
	defer zr.Close()

	binaryName := platform.BinaryName()
	for _, f := range zr.File {
		if filepath.Base(f.Name) != binaryName {
			continue
		}

		rc, err := f.Open()
		if err != nil {
			return "", fmt.Errorf("open zip entry %s: %w", f.Name, err)
		}

		destPath := filepath.Join(destDir, binaryName)
		out, err := os.Create(destPath)
		if err != nil {
			rc.Close()
			return "", fmt.Errorf("create binary file: %w", err)
		}

		if _, err := io.Copy(out, rc); err != nil {
			out.Close()
			rc.Close()
			os.Remove(destPath)
			return "", fmt.Errorf("extract binary: %w", err)
		}
		out.Close()
		rc.Close()

		if err := os.Chmod(destPath, 0o755); err != nil {
			return "", fmt.Errorf("chmod binary: %w", err)
		}
		return destPath, nil
	}

	return "", fmt.Errorf("binary %q not found in ZIP archive", binaryName)
}
