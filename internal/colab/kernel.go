package colab

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/gorilla/websocket"
)

type msgHeader struct {
	MsgID    string `json:"msg_id"`
	Username string `json:"username"`
	Session  string `json:"session"`
	Date     string `json:"date"`
	MsgType  string `json:"msg_type"`
	Version  string `json:"version"`
}

type jupyterMsg struct {
	Header       msgHeader              `json:"header"`
	ParentHeader msgHeader              `json:"parent_header"`
	Metadata     map[string]interface{} `json:"metadata"`
	Content      map[string]interface{} `json:"content"`
	Channel      string                 `json:"channel"`
	Buffers      []interface{}          `json:"buffers"`
}

type OutputKind string

const (
	OutputStdout   OutputKind = "stdout"
	OutputStderr   OutputKind = "stderr"
	OutputResult   OutputKind = "result"
	OutputDisplay  OutputKind = "display"
	OutputError    OutputKind = "error"
	OutputStatus   OutputKind = "status"
	OutputInputReq OutputKind = "input_request"
)

type KernelOutput struct {
	Kind       OutputKind
	Text       string
	MsgType    string
	Filename   string            // suggested name from display metadata, if any
	BinaryData map[string][]byte // mime type → decoded bytes (images, audio, etc.)
}

// Kernel holds a live WebSocket connection to a Jupyter kernel.
// A background goroutine continuously reads all frames so that server pings
// are answered even while the REPL is idle waiting for user input.
type Kernel struct {
	conn      *websocket.Conn
	kernelID  string
	sessionID string
	msgs      chan jupyterMsg
	done      chan struct{}
}

func CreateAndConnectKernel(ctx context.Context, info *AssignmentInfo) (*Kernel, error) {
	proxyToken := info.RuntimeProxyInfo.Token
	baseURL := info.JupyterBaseURL()

	kernelID, _, err := acquireKernel(ctx, baseURL, proxyToken)
	if err != nil {
		return nil, fmt.Errorf("acquire kernel: %w", err)
	}

	sessionID, err := randomHex(16)
	if err != nil {
		return nil, err
	}

	wsURL := info.KernelWebSocketURL(kernelID, sessionID)

	wsHeaders := http.Header{}
	wsHeaders.Set("Origin", "https://colab.research.google.com")
	wsHeaders.Set("X-Colab-Client-Agent", "colab-cli")
	wsHeaders.Set("X-Colab-Runtime-Proxy-Token", proxyToken)

	conn, resp, err := websocket.DefaultDialer.DialContext(ctx, wsURL, wsHeaders)
	if err != nil {
		if resp != nil {
			b, _ := io.ReadAll(io.LimitReader(resp.Body, 2048))
			return nil, fmt.Errorf("dial kernel ws (HTTP %d): %w — %s", resp.StatusCode, err, string(b))
		}
		return nil, fmt.Errorf("dial kernel ws: %w", err)
	}

	k := &Kernel{
		conn:      conn,
		kernelID:  kernelID,
		sessionID: sessionID,
		msgs:      make(chan jupyterMsg, 256),
		done:      make(chan struct{}),
	}

	go k.readLoop()

	return k, nil
}

// readLoop runs in a goroutine for the lifetime of the kernel connection.
// It reads every frame from the WebSocket (keeping pings answered) and
// forwards parsed messages to k.msgs.
func (k *Kernel) readLoop() {
	defer close(k.msgs)
	for {
		select {
		case <-k.done:
			return
		default:
		}
		k.conn.SetReadDeadline(time.Time{})
		msgType, raw, err := k.conn.ReadMessage()
		if err != nil {
			return
		}

		var jsonBytes []byte
		if msgType == websocket.TextMessage {
			// Default protocol, no buffers: plain JSON text frame.
			jsonBytes = raw
		} else {
			// Default protocol, binary frame:
			//   bytes 0-3:        nbufs (big-endian uint32)
			//   bytes 4..4+4n-1:  offsets[0..n-1] (big-endian uint32 each)
			//   offsets[0]:       start of buffer[0] (JSON)
			//   offsets[1]:       start of buffer[1] (= end of JSON)
			if len(raw) < 8 {
				continue
			}
			nbufs := binary.BigEndian.Uint32(raw[0:4])
			if nbufs < 2 {
				continue
			}
			headerLen := 4 + 4*int(nbufs)
			if len(raw) < headerLen {
				continue
			}
			off0 := int(binary.BigEndian.Uint32(raw[4:8]))
			off1 := int(binary.BigEndian.Uint32(raw[8:12]))
			if off0 < 0 || off1 > len(raw) || off0 > off1 {
				continue
			}
			jsonBytes = raw[off0:off1]
		}

		var msg jupyterMsg
		if err := json.Unmarshal(jsonBytes, &msg); err != nil {
			continue
		}
		select {
		case k.msgs <- msg:
		case <-k.done:
			return
		}
	}
}

func (k *Kernel) waitReady(ctx context.Context) error {
	msgID, err := randomHex(16)
	if err != nil {
		return err
	}

	req := jupyterMsg{
		Header: msgHeader{
			MsgID:    msgID,
			Username: "colab-cli",
			Session:  k.sessionID,
			Date:     time.Now().UTC().Format(time.RFC3339),
			MsgType:  "kernel_info_request",
			Version:  "5.3",
		},
		ParentHeader: msgHeader{},
		Metadata:     map[string]interface{}{},
		Content:      map[string]interface{}{},
		Channel:      "shell",
		Buffers:      []interface{}{},
	}
	data, err := json.Marshal(req)
	if err != nil {
		return err
	}
	if err := k.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return err
	}

	deadline := time.NewTimer(3 * time.Minute)
	defer deadline.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-deadline.C:
			return fmt.Errorf("timed out waiting for kernel_info_reply")
		case msg, ok := <-k.msgs:
			if !ok {
				return fmt.Errorf("kernel connection closed while waiting for kernel_info_reply")
			}
			if msg.Header.MsgType == "kernel_info_reply" && msg.ParentHeader.MsgID == msgID {
				return nil
			}
		}
	}
}

func (k *Kernel) Execute(ctx context.Context, code string) (<-chan KernelOutput, error) {
	msgID, err := randomHex(16)
	if err != nil {
		return nil, err
	}

	req := jupyterMsg{
		Header: msgHeader{
			MsgID:    msgID,
			Username: "colab-cli",
			Session:  k.sessionID,
			Date:     time.Now().UTC().Format(time.RFC3339),
			MsgType:  "execute_request",
			Version:  "5.3",
		},
		ParentHeader: msgHeader{},
		Metadata:     map[string]interface{}{},
		Content: map[string]interface{}{
			"code":             code,
			"silent":           false,
			"store_history":    true,
			"user_expressions": map[string]interface{}{},
			"allow_stdin":      false,
			"stop_on_error":    true,
		},
		Channel: "shell",
		Buffers: []interface{}{},
	}
	data, err := json.Marshal(req)
	if err != nil {
		return nil, err
	}
	if err := k.conn.WriteMessage(websocket.TextMessage, data); err != nil {
		return nil, fmt.Errorf("send execute_request: %w", err)
	}

	outCh := make(chan KernelOutput, 32)

	go func() {
		defer close(outCh)

		const idleTimeout = 10 * time.Minute
		timer := time.NewTimer(idleTimeout)
		defer timer.Stop()

		resetTimer := func() {
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(idleTimeout)
		}

		for {
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
				return
			case msg, ok := <-k.msgs:
				if !ok {
					return
				}
				resetTimer()
				if msg.ParentHeader.MsgID != msgID {
					continue
				}
				switch msg.Header.MsgType {
				case "stream":
					text, _ := msg.Content["text"].(string)
					kind := OutputStdout
					if name, _ := msg.Content["name"].(string); name == "stderr" {
						kind = OutputStderr
					}
					outCh <- KernelOutput{Kind: kind, Text: text, MsgType: msg.Header.MsgType}
				case "execute_result":
					if d, ok := msg.Content["data"].(map[string]interface{}); ok {
						out := KernelOutput{Kind: OutputResult, MsgType: msg.Header.MsgType}
						extractBinaryData(&out, d)
						out.Filename = filenameFromMetadata(msg.Content["metadata"])
						if txt, ok := d["text/plain"].(string); ok {
							out.Text = txt
						}
						outCh <- out
					}
				case "display_data":
					if d, ok := msg.Content["data"].(map[string]interface{}); ok {
						out := KernelOutput{Kind: OutputDisplay, MsgType: msg.Header.MsgType}
						extractBinaryData(&out, d)
						out.Filename = filenameFromMetadata(msg.Content["metadata"])
						if txt, ok := d["text/plain"].(string); ok {
							out.Text = txt
						}
						outCh <- out
					}
				case "error":
					ename, _ := msg.Content["ename"].(string)
					evalue, _ := msg.Content["evalue"].(string)
					text := ename + ": " + evalue
					if tb, ok := msg.Content["traceback"].([]interface{}); ok {
						lines := make([]string, 0, len(tb))
						for _, l := range tb {
							if s, ok := l.(string); ok {
								lines = append(lines, stripANSI(s))
							}
						}
						text += "\n" + strings.Join(lines, "\n")
					}
					outCh <- KernelOutput{Kind: OutputError, Text: text, MsgType: msg.Header.MsgType}
				case "status":
					state, _ := msg.Content["execution_state"].(string)
					outCh <- KernelOutput{Kind: OutputStatus, Text: state, MsgType: msg.Header.MsgType}
					if state == "idle" {
						return
					}
				case "input_request":
					prompt, _ := msg.Content["prompt"].(string)
					outCh <- KernelOutput{Kind: OutputInputReq, Text: prompt, MsgType: msg.Header.MsgType}
				case "execute_reply":
					if status, _ := msg.Content["status"].(string); status == "error" {
						ename, _ := msg.Content["ename"].(string)
						evalue, _ := msg.Content["evalue"].(string)
						outCh <- KernelOutput{
							Kind:    OutputError,
							Text:    ename + ": " + evalue,
							MsgType: msg.Header.MsgType,
						}
					}
				}
			}
		}
	}()

	return outCh, nil
}

func (k *Kernel) Close() error {
	close(k.done)
	return k.conn.Close()
}

func acquireKernel(ctx context.Context, baseURL, proxyToken string) (string, bool, error) {
	ids, err := listKernels(ctx, baseURL, proxyToken)
	if err == nil && len(ids) > 0 {
		return ids[0], false, nil
	}
	id, err := createKernel(ctx, baseURL, proxyToken)
	return id, true, err
}

func listKernels(ctx context.Context, baseURL, proxyToken string) ([]string, error) {
	u := fmt.Sprintf("%s/api/kernels?token=%s", baseURL, url.QueryEscape(proxyToken))
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", "token "+proxyToken)
	req.Header.Set("X-Colab-Client-Agent", "colab-cli")
	req.Header.Set("X-Colab-Runtime-Proxy-Token", proxyToken)

	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	var kernels []struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &kernels); err != nil {
		return nil, err
	}
	ids := make([]string, 0, len(kernels))
	for _, k := range kernels {
		if k.ID != "" {
			ids = append(ids, k.ID)
		}
	}
	return ids, nil
}

func createKernel(ctx context.Context, baseURL, proxyToken string) (string, error) {
	u := fmt.Sprintf("%s/api/kernels?token=%s", baseURL, url.QueryEscape(proxyToken))
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, u, strings.NewReader("{}"))
	if err != nil {
		return "", err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "token "+proxyToken)
	req.Header.Set("X-Colab-Client-Agent", "colab-cli")
	req.Header.Set("X-Colab-Runtime-Proxy-Token", proxyToken)

	resp, err := (&http.Client{Timeout: 60 * time.Second}).Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("HTTP %d: %s", resp.StatusCode, string(body))
	}
	var kernel struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal(body, &kernel); err != nil || kernel.ID == "" {
		return "", fmt.Errorf("unexpected create kernel response: %s", string(body))
	}
	return kernel.ID, nil
}

// binaryMIMETypes lists MIME types whose values are base64-encoded binary data.
var binaryMIMETypes = []string{
	"image/png",
	"image/jpeg",
	"image/gif",
	"image/svg+xml",
	"audio/wav",
	"audio/mpeg",
	"audio/mp3",
	"audio/ogg",
}

// filenameFromMetadata pulls a caller-supplied name out of a display message's
// metadata. The convention is display(obj, metadata={"filename": "name.ext"}).
// A "colab_filename" key is also accepted. Only the base name is kept so the
// metadata cannot direct writes outside the chosen output directory.
func filenameFromMetadata(meta interface{}) string {
	m, ok := meta.(map[string]interface{})
	if !ok {
		return ""
	}
	for _, key := range []string{"filename", "colab_filename"} {
		if name, ok := m[key].(string); ok && name != "" {
			return filepath.Base(name)
		}
	}
	return ""
}

// dataURIPattern matches a base64 data URI such as the ones IPython embeds in
// HTML for Audio and Video widgets: data:audio/wav;base64,<payload>.
var dataURIPattern = regexp.MustCompile(`data:([a-zA-Z0-9.+-]+/[a-zA-Z0-9.+-]+);base64,([A-Za-z0-9+/=]+)`)

// extractBinaryData decodes any base64-encoded binary fields from a Jupyter
// data dict (the "data" key of display_data / execute_result) into out.BinaryData.
func extractBinaryData(out *KernelOutput, data map[string]interface{}) {
	store := func(mime string, b []byte) {
		if out.BinaryData == nil {
			out.BinaryData = make(map[string][]byte)
		}
		out.BinaryData[mime] = b
	}

	for _, mime := range binaryMIMETypes {
		raw, ok := data[mime].(string)
		if !ok || raw == "" {
			continue
		}
		b, err := base64.StdEncoding.DecodeString(raw)
		if err != nil {
			continue
		}
		store(mime, b)
	}

	// Some rich displays (IPython.display.Audio and Video, for example) provide
	// only an HTML representation with the media embedded as a base64 data URI,
	// not a top-level binary MIME bundle. Pull those out so they can be saved.
	html, ok := data["text/html"].(string)
	if !ok || html == "" {
		return
	}
	for _, m := range dataURIPattern.FindAllStringSubmatch(html, -1) {
		mime, payload := m[1], m[2]
		if !strings.HasPrefix(mime, "image/") &&
			!strings.HasPrefix(mime, "audio/") &&
			!strings.HasPrefix(mime, "video/") {
			continue
		}
		// A top-level bundle for this MIME type is higher fidelity; keep it.
		if _, exists := out.BinaryData[mime]; exists {
			continue
		}
		b, err := base64.StdEncoding.DecodeString(payload)
		if err != nil {
			continue
		}
		store(mime, b)
	}
}

func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func stripANSI(s string) string {
	var out strings.Builder
	i := 0
	for i < len(s) {
		if s[i] == '\x1b' && i+1 < len(s) && s[i+1] == '[' {
			i += 2
			for i < len(s) && s[i] != 'm' {
				i++
			}
			i++
			continue
		}
		out.WriteByte(s[i])
		i++
	}
	return out.String()
}
