package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/tetsuo/colabctl/internal/colab"
)

// mimeExtensions maps MIME types to file extensions for binary outputs.
var mimeExtensions = map[string]string{
	"image/png":     ".png",
	"image/jpeg":    ".jpg",
	"image/gif":     ".gif",
	"image/svg+xml": ".svg",
	"audio/wav":     ".wav",
	"audio/mpeg":    ".mp3",
	"audio/mp3":     ".mp3",
	"audio/ogg":     ".ogg",
	"video/mp4":     ".mp4",
	"video/webm":    ".webm",
}

// mimeOrder controls which MIME type is saved when a message carries several
// (prefer the highest-quality representation).
var mimeOrder = []string{
	"image/png", "image/jpeg", "image/gif", "image/svg+xml",
	"audio/wav", "audio/mpeg", "audio/mp3", "audio/ogg",
	"video/mp4", "video/webm",
}

// printOutputs drains the channel of kernel outputs and writes them to the
// terminal. stdout/result go to os.Stdout; errors/stderr go to os.Stderr.
// Binary outputs (images, audio, video) are saved to files. They go in
// outputDir when it is set, otherwise the current directory. The function
// blocks until the channel is closed.
func printOutputs(ch <-chan colab.KernelOutput, outputDir string) {
	fileCount := 0
	for out := range ch {
		// One displayed object can arrive in several encodings (a figure as
		// both PNG and JPEG, for example). Save only the highest-priority one
		// so each object yields a single file.
		for _, mime := range mimeOrder {
			data, ok := out.BinaryData[mime]
			if !ok {
				continue
			}
			ext := mimeExtensions[mime]
			fileCount++
			// Prefer a name the cell supplied via display metadata; otherwise
			// fall back to a numbered default.
			name := fmt.Sprintf("colab-output-%d%s", fileCount, ext)
			if out.Filename != "" {
				name = out.Filename
			}
			if outputDir != "" {
				if err := os.MkdirAll(outputDir, 0o755); err != nil {
					fmt.Fprintf(os.Stderr, "error creating %s: %v\n", outputDir, err)
				}
				name = filepath.Join(outputDir, name)
			}
			if err := os.WriteFile(name, data, 0o644); err != nil {
				fmt.Fprintf(os.Stderr, "error saving %s: %v\n", mime, err)
			} else {
				fmt.Fprintf(os.Stderr, "Saved: %s\n", name)
			}
			break
		}

		switch out.Kind {
		case colab.OutputStdout, colab.OutputResult:
			fmt.Print(out.Text)
			if len(out.Text) > 0 && out.Text[len(out.Text)-1] != '\n' {
				fmt.Println()
			}
		case colab.OutputDisplay:
			// Only print the text repr when there was no binary output saved.
			if len(out.BinaryData) == 0 && out.Text != "" {
				fmt.Print(out.Text)
				if out.Text[len(out.Text)-1] != '\n' {
					fmt.Println()
				}
			}
		case colab.OutputStderr:
			fmt.Fprint(os.Stderr, out.Text)
			if len(out.Text) > 0 && out.Text[len(out.Text)-1] != '\n' {
				fmt.Fprintln(os.Stderr)
			}
		case colab.OutputError:
			fmt.Fprintln(os.Stderr, out.Text)
		case colab.OutputStatus:
			// busy/idle - not printed to the user.
		case colab.OutputInputReq:
			fmt.Printf("Input requested: %s", out.Text)
		}
	}
}

// notebookHash returns a fresh random notebook hash in the web-safe encoding
// that the Colab assign endpoint expects as the nbh query parameter.
//
// Format: UUID-like hex string with dashes replaced by underscores, padded
// with dots to 44 characters (mirrors uuid_to_web_safe_base64 in colab-cli).
func notebookHash() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	// Format as UUID groups: 8-4-4-4-12
	h := hex.EncodeToString(b)
	uuid := h[0:8] + "-" + h[8:12] + "-" + h[12:16] + "-" + h[16:20] + "-" + h[20:32]
	// Replace dashes with underscores and pad to 44 chars with dots.
	encoded := strings.ReplaceAll(uuid, "-", "_")
	encoded += strings.Repeat(".", 44-len(uuid))
	return encoded
}
