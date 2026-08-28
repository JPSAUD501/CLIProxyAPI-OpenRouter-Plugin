package openrouter

import (
	"bytes"
	"strings"

	"github.com/tidwall/gjson"
	"github.com/tidwall/sjson"
)

type sseDecoder struct {
	buffer []byte
}

func (d *sseDecoder) Feed(chunk []byte) [][]byte {
	d.buffer = append(d.buffer, chunk...)
	var frames [][]byte
	for {
		index, delimiter := nextFrameDelimiter(d.buffer)
		if index < 0 {
			return frames
		}
		end := index + delimiter
		frames = append(frames, append([]byte(nil), d.buffer[:end]...))
		d.buffer = d.buffer[end:]
	}
}

func (d *sseDecoder) Flush() []byte {
	out := append([]byte(nil), d.buffer...)
	d.buffer = nil
	return out
}

func nextFrameDelimiter(value []byte) (int, int) {
	lf := bytes.Index(value, []byte("\n\n"))
	crlf := bytes.Index(value, []byte("\r\n\r\n"))
	switch {
	case lf < 0:
		if crlf < 0 {
			return -1, 0
		}
		return crlf, 4
	case crlf < 0 || lf < crlf:
		return lf, 2
	default:
		return crlf, 4
	}
}

func rewriteSSEFrame(frame []byte) []byte {
	separator := "\n"
	if bytes.Contains(frame, []byte("\r\n")) {
		separator = "\r\n"
	}
	ending := ""
	body := string(frame)
	if strings.HasSuffix(body, separator+separator) {
		ending = separator + separator
		body = strings.TrimSuffix(body, ending)
	}
	lines := strings.Split(body, separator)
	changed := false
	for index, line := range lines {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		payload := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
		if payload == "" || payload == "[DONE]" {
			continue
		}
		rewritten, ok := rewriteResponseModels([]byte(payload))
		if ok {
			lines[index] = "data: " + string(rewritten)
			changed = true
		}
	}
	if !changed {
		return frame
	}
	return []byte(strings.Join(lines, separator) + ending)
}

// openAIStreamPayload returns the data payload expected by CLIProxyAPI's
// OpenAI chat-completions handler. The host owns the downstream SSE framing,
// so forwarding OpenRouter's complete SSE frame would produce
// "data: data: {...}" and turn comment heartbeats into invalid JSON chunks.
func openAIStreamPayload(frame []byte) ([]byte, bool) {
	separator := "\n"
	if bytes.Contains(frame, []byte("\r\n")) {
		separator = "\r\n"
	}
	body := string(frame)
	body = strings.TrimSuffix(body, separator+separator)
	dataLines := make([]string, 0, 1)
	for _, line := range strings.Split(body, separator) {
		if !strings.HasPrefix(line, "data:") {
			continue
		}
		value := strings.TrimPrefix(line, "data:")
		if strings.HasPrefix(value, " ") {
			value = strings.TrimPrefix(value, " ")
		}
		dataLines = append(dataLines, value)
	}
	if len(dataLines) == 0 {
		return nil, false
	}
	payload := []byte(strings.Join(dataLines, "\n"))
	if len(bytes.TrimSpace(payload)) == 0 || bytes.Equal(bytes.TrimSpace(payload), []byte("[DONE]")) {
		return nil, false
	}
	if rewritten, ok := rewriteResponseModels(payload); ok {
		payload = rewritten
	}
	return payload, true
}

func rewriteRequestModel(body []byte, native string) ([]byte, error) {
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return nil, statusError("invalid_request", "request body must be a JSON object", 400, false)
	}
	rewritten, err := sjson.SetBytes(body, "model", native)
	if err != nil {
		return nil, statusError("invalid_request", "request model could not be rewritten", 400, false)
	}
	return rewritten, nil
}

func rewriteRequestEffort(body []byte, effort string) ([]byte, error) {
	rewritten, err := sjson.SetBytes(body, "reasoning.effort", effort)
	if err != nil {
		return nil, statusError("invalid_request", "request reasoning effort could not be applied", 400, false)
	}
	return rewritten, nil
}

func rewriteResponseModels(body []byte) ([]byte, bool) {
	if !gjson.ValidBytes(body) || !gjson.ParseBytes(body).IsObject() {
		return body, false
	}
	rewritten := body
	changed := false
	for _, path := range []string{"model", "response.model", "message.model"} {
		model := gjson.GetBytes(rewritten, path)
		if !model.Exists() || model.Type != gjson.String {
			continue
		}
		alias := modelAlias(model.String())
		if alias == model.String() {
			continue
		}
		next, err := sjson.SetBytes(rewritten, path, alias)
		if err == nil {
			rewritten = next
			changed = true
		}
	}
	if !changed {
		return body, false
	}
	return rewritten, true
}
