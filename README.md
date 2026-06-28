# colabctl

A command-line client for [Google Colab](https://colab.research.google.com/) written in Go.

It lets you list, run, and interact with your notebooks from the terminal.

## Installation

```sh
go install github.com/tetsuo/colabctl@latest
```

## Getting started

### 1. Create OAuth2 credentials

1. Go to <https://console.cloud.google.com/apis/credentials>.
2. Create an **OAuth 2.0 Client ID** of type **Desktop application**.
3. Enable the **Google Drive API** for your project.
4. Download or copy the **Client ID** and **Client Secret**.
5. Add yourself to the tests users if your app is sandboxed.

### 2. Authenticate

```sh
export COLAB_CLIENT_ID=your-client-id
export COLAB_CLIENT_SECRET=your-client-secret
colabctl auth login
```

A browser window opens for Google sign-in. After approval, the token is stored locally in `~/.config/colab/sessions.json`, so you will not need to sign in again until the refresh token expires.

Alternatively, pass credentials once:

```sh
colabctl auth login --client-id <id> --client-secret <secret>
```

## Usage

```sh
# List notebooks
colabctl list

# Run all cells in a notebook (get the ID from colabctl list)
colabctl run ID

# Interactive REPL
colabctl exec ID

# Run on T4 GPU
echo '!nvidia-smi' | colabctl exec ID --accelerator T4

# Copy files to/from the active runtime
colabctl cp :/content/model.pt ./model.pt        # download
colabctl cp train.py :/content/train.py          # upload

# A directory is not copied directly; archive it on the runtime first.
#   colabctl exec <id> <<< '!tar czf /content/out.tgz -C /content/model .'
#   colabctl cp :content/out.tgz ./out.tgz

# Manage sessions
colabctl sessions
colabctl stop ID

# Check auth status
colabctl auth status

# Log out
colabctl auth logout
```

### REPL multi-line input

End a line with `\` to continue on the next line:

```
>>> for i in range(3): \
...     print(i)
0
1
2
```

## How it works

1. `auth` starts a temporary localhost server for code exchange during authorization. The received refresh token is persisted to your HOME folder and reused automatically.
2. `list` queries the Google Drive API v3 for files with MIME type `application/vnd.google.colaboratory`.
3. `run` / `exec` calls the Colab session API (`POST /api/sessions`) to start a runtime, then connects to the Jupyter kernel over WebSocket using the Jupyter wire protocol (v5) to send `execute_request` messages and stream back output.
4. `cp` uses the Jupyter Contents API (`/api/contents`) over the runtime proxy to download or upload a single file. Paths are relative to the Jupyter server root. Directories are not handled directly; archive them on the runtime (`tar`) and copy the archive.

## Examples

Run GPU benchmark:

```sh
$ cat examples/torch_gpu_benchmark.py | colabctl exec ID --accelerator T4

Device: Tesla T4
Torch : 2.11.0+cu128
4096x4096 matmul: 32.98 ms/iter, 4.2 TFLOP/s (fp32)
```

Generate an image from a text prompt using SD-Turbo:

```sh
cat examples/txt2img.py | colabctl exec ID --accelerator T4 -o examples
```

![fluffy-creature](./examples/a_hyper_detailed_ultra_fluffy_fantasy_cr.png)

SD-Turbo is about 2.5 GB and loads only on the first call; because the kernel persists between `exec` calls, later prompts reuse the loaded pipeline and finish in a second or two.
