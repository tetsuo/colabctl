package main

import (
	"context"
	"fmt"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/spf13/cobra"

	"github.com/tetsuo/colabctl/internal/auth"
	"github.com/tetsuo/colabctl/internal/colab"
)

// remoteSpec is a parsed copy endpoint. When isRemote is true the location
// lives on a runtime; endpoint may be empty to mean "the only active runtime".
type remoteSpec struct {
	isRemote bool
	endpoint string
	path     string
}

// parseSpec splits an scp-style argument. An argument is treated as remote when
// it contains a colon that comes before any slash, mirroring scp. The part
// before the colon is the runtime endpoint, or empty for the active runtime.
func parseSpec(arg string) remoteSpec {
	idx := strings.IndexByte(arg, ':')
	if idx < 0 || strings.IndexByte(arg[:idx], '/') >= 0 {
		return remoteSpec{isRemote: false, path: arg}
	}
	return remoteSpec{isRemote: true, endpoint: arg[:idx], path: arg[idx+1:]}
}

var cpCmd = &cobra.Command{
	Use:   "cp <src> <dst>",
	Short: "Copy files between your machine and a Colab runtime (scp-style)",
	Long: `Copy a file to or from an active Colab runtime, using scp-style paths.

A remote location is written as endpoint:path. Leave the endpoint empty to use
the only active runtime:

  # Download a trained model from the active runtime.
  colabctl cp :/content/model.pt ./model.pt

  # Upload a training script to a specific runtime.
  colabctl cp train.py gpu-t4-s-abc123:/content/train.py

Get the endpoint from 'colabctl sessions'. Directories are not copied directly;
archive them on the runtime first (tar) and copy the archive.`,
	Args: cobra.ExactArgs(2),
	RunE: func(cmd *cobra.Command, args []string) error {
		requireCredentials()
		ctx := cmd.Context()

		src := parseSpec(args[0])
		dst := parseSpec(args[1])

		if src.isRemote == dst.isRemote {
			if src.isRemote {
				return fmt.Errorf("copying between two runtimes is not supported")
			}
			return fmt.Errorf("at least one path must be remote (endpoint:path); use cp(1) for local copies")
		}

		httpClient, err := auth.Client(ctx, clientID, clientSecret)
		if err != nil {
			return err
		}
		client := colab.New(httpClient)

		if src.isRemote {
			return download(ctx, client, src, dst.path)
		}
		return upload(ctx, client, src.path, dst)
	},
}

// resolveRuntime finds an active runtime. With an explicit endpoint it looks
// that one up; otherwise it requires exactly one active runtime.
func resolveRuntime(ctx context.Context, client *colab.Client, endpoint string) (*colab.AssignmentInfo, error) {
	if endpoint != "" {
		return client.AssignmentFromEndpoint(ctx, endpoint)
	}

	runtimes, err := client.ListAssignments(ctx)
	if err != nil {
		return nil, err
	}
	switch len(runtimes) {
	case 0:
		return nil, fmt.Errorf("no active runtimes: start one with 'colabctl exec', or name one as endpoint:path")
	case 1:
		return client.AssignmentFromEndpoint(ctx, runtimes[0].Endpoint)
	default:
		var b strings.Builder
		fmt.Fprintf(&b, "%d active runtimes: name one in the path as endpoint:path:\n", len(runtimes))
		for _, r := range runtimes {
			fmt.Fprintf(&b, "  %s\n", r.Endpoint)
		}
		return nil, fmt.Errorf("%s", b.String())
	}
}

func download(ctx context.Context, client *colab.Client, src remoteSpec, localPath string) error {
	assignment, err := resolveRuntime(ctx, client, src.endpoint)
	if err != nil {
		return err
	}

	fc, err := client.FetchFile(ctx, assignment, src.path)
	if err != nil {
		return err
	}

	// If the local target is a directory (or ends with a separator), keep the
	// remote file name inside it.
	target := localPath
	if target == "" || strings.HasSuffix(target, string(os.PathSeparator)) {
		target = filepath.Join(target, fc.Name)
	} else if fi, statErr := os.Stat(target); statErr == nil && fi.IsDir() {
		target = filepath.Join(target, fc.Name)
	}

	if dir := filepath.Dir(target); dir != "" {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
	}
	if err := os.WriteFile(target, fc.Data, 0o644); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Downloaded %s (%d bytes) -> %s\n", src.path, len(fc.Data), target)
	return nil
}

func upload(ctx context.Context, client *colab.Client, localPath string, dst remoteSpec) error {
	data, err := os.ReadFile(localPath)
	if err != nil {
		return err
	}

	assignment, err := resolveRuntime(ctx, client, dst.endpoint)
	if err != nil {
		return err
	}

	// If the remote target names a directory, keep the local file name.
	remotePath := dst.path
	if remotePath == "" || strings.HasSuffix(remotePath, "/") {
		remotePath = path.Join(remotePath, filepath.Base(localPath))
	}

	if err := client.PutFile(ctx, assignment, remotePath, data); err != nil {
		return err
	}
	fmt.Fprintf(os.Stderr, "Uploaded %s (%d bytes) -> %s\n", localPath, len(data), remotePath)
	return nil
}

func init() {
	rootCmd.AddCommand(cpCmd)
}
