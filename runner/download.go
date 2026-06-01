package runner

import (
	"archive/tar"
	"archive/zip"
	"compress/gzip"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
)

const githubLatestRelease = "https://api.github.com/repos/ggerganov/llama.cpp/releases/latest"

// ProgressUpdate is emitted by the download goroutine on each chunk received,
// and once more as the final message when extraction completes (Done=true).
type ProgressUpdate struct {
	Downloaded int64  // bytes received so far
	Total      int64  // total bytes; 0 = unknown (no Content-Length)
	Done       bool   // true on the final message only
	Err        error  // set on the final message when something went wrong
	BinaryPath string // set on the final message when Done && Err == nil
}

// StartDownload launches the download goroutine and returns a read-only channel
// of ProgressUpdate values. Drain it with DownloadProgressCmd.
//
// ctx can be cancelled to abort the download; the cancellation error is
// delivered on the final ProgressUpdate (Done=true, Err=ctx.Err()).
//
// The channel is closed after the final message is sent.
func StartDownload(ctx context.Context, platform Platform, destDir string) <-chan ProgressUpdate {
	ch := make(chan ProgressUpdate, 4)
	go func() {
		defer close(ch)
		binaryPath, err := downloadAndExtract(ctx, platform, destDir, ch)
		// The goroutine wrapper is the single sender of the final Done message.
		ch <- ProgressUpdate{Done: true, Err: err, BinaryPath: binaryPath}
	}()
	return ch
}

// DownloadProgressCmd returns a tea.Cmd that blocks until the next ProgressUpdate
// arrives on ch, then returns it as a DownloadProgressMsg.
//
// Re-issue this cmd after each message where Done == false.
// When the channel is closed (after Done == true), the zero-value ProgressUpdate
// is returned, which the caller should treat as terminal.
func DownloadProgressCmd(ch <-chan ProgressUpdate) tea.Cmd {
	return func() tea.Msg {
		return DownloadProgressMsg(<-ch)
	}
}

// downloadAndExtract performs the full pipeline: GitHub API lookup → streaming
// download to temp file → ZIP extraction. Progress updates are written to ch.
//
// Returns the path to the extracted binary on success.
// The final Done=true ProgressUpdate is NOT sent here — the caller (StartDownload)
// sends it so there is exactly one sender of the terminal message.
func downloadAndExtract(ctx context.Context, platform Platform, destDir string, ch chan<- ProgressUpdate) (string, error) {
	// 1. Resolve the download URL from the GitHub Releases API.
	url, totalSize, err := resolveAssetURL(ctx, platform)
	if err != nil {
		return "", fmt.Errorf("resolve release asset: %w", err)
	}

	// 2. Stream the archive to a temp file, emitting progress updates.
	tmpExt := ".zip"
	if strings.HasSuffix(url, ".tar.gz") {
		tmpExt = ".tar.gz"
	}
	tmpFile, err := os.CreateTemp("", "livie-llama-*"+tmpExt)
	if err != nil {
		return "", fmt.Errorf("create temp file: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer os.Remove(tmpPath)

	if err := downloadToFile(ctx, url, totalSize, tmpFile, ch); err != nil {
		tmpFile.Close()
		return "", err
	}
	tmpFile.Close()

	// 3. Extract the binary from the ZIP.
	if err := os.MkdirAll(destDir, 0o755); err != nil {
		return "", fmt.Errorf("mkdir %s: %w", destDir, err)
	}

	binaryPath, err := extractBinary(tmpPath, url, platform, destDir)
	if err != nil {
		return "", fmt.Errorf("extract binary: %w", err)
	}

	return binaryPath, nil
}

// resolveAssetURL calls the GitHub Releases API and returns the browser download
// URL and byte size of the ZIP asset matching platform.ReleaseAssetSuffix().
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
		if strings.HasSuffix(a.Name, suffix) {
			return a.BrowserDownloadURL, a.Size, nil
		}
	}
	return "", 0, fmt.Errorf("no release asset with suffix %q", suffix)
}

// downloadToFile streams url into f, writing non-blocking ProgressUpdates to ch.
//
// Progress updates are sent with a non-blocking select: if the channel's 4-slot
// buffer is full (consumer is behind), the update is dropped. The consumer
// (DownloadProgressCmd) processes one update per tea.Cmd cycle, so at most a
// few updates per second may be skipped under heavy traffic. This does not
// affect correctness — the final Done message is sent unconditionally.
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

// extractBinary opens the archive at archivePath (ZIP or tar.gz, detected from
// the original download url), finds the llama-server binary by name, extracts
// it to destDir, and sets executable permissions.
func extractBinary(archivePath, url string, platform Platform, destDir string) (string, error) {
	if strings.HasSuffix(url, ".tar.gz") {
		return extractBinaryTarGz(archivePath, platform, destDir)
	}
	return extractBinaryZip(archivePath, platform, destDir)
}

// extractBinaryZip extracts the llama-server binary from a ZIP archive.
func extractBinaryZip(zipPath string, platform Platform, destDir string) (string, error) {
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
			return "", fmt.Errorf("create %s: %w", destPath, err)
		}

		if _, err := io.Copy(out, rc); err != nil {
			out.Close()
			rc.Close()
			os.Remove(destPath)
			return "", fmt.Errorf("write %s: %w", destPath, err)
		}
		out.Close()
		rc.Close()

		if err := os.Chmod(destPath, 0o755); err != nil {
			return "", fmt.Errorf("chmod %s: %w", destPath, err)
		}
		return destPath, nil
	}

	return "", fmt.Errorf("binary %q not found in ZIP", binaryName)
}

// extractBinaryTarGz extracts the llama-server binary from a .tar.gz archive.
func extractBinaryTarGz(archivePath string, platform Platform, destDir string) (string, error) {
	f, err := os.Open(archivePath)
	if err != nil {
		return "", fmt.Errorf("open tar.gz: %w", err)
	}
	defer f.Close()

	gr, err := gzip.NewReader(f)
	if err != nil {
		return "", fmt.Errorf("gzip reader: %w", err)
	}
	defer gr.Close()

	tr := tar.NewReader(gr)
	binaryName := platform.BinaryName()

	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return "", fmt.Errorf("read tar: %w", err)
		}
		if hdr.Typeflag != tar.TypeReg {
			continue
		}
		if filepath.Base(hdr.Name) != binaryName {
			continue
		}

		destPath := filepath.Join(destDir, binaryName)
		out, err := os.Create(destPath)
		if err != nil {
			return "", fmt.Errorf("create %s: %w", destPath, err)
		}

		if _, err := io.Copy(out, tr); err != nil {
			out.Close()
			os.Remove(destPath)
			return "", fmt.Errorf("write %s: %w", destPath, err)
		}
		out.Close()

		if err := os.Chmod(destPath, 0o755); err != nil {
			return "", fmt.Errorf("chmod %s: %w", destPath, err)
		}
		return destPath, nil
	}

	return "", fmt.Errorf("binary %q not found in tar.gz", binaryName)
}
