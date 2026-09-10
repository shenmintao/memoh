package message

import "bytes"

// JSONB cannot represent U+0000. Keep a visible control-picture in the chat
// projection instead of rejecting the entire round. Walk JSON escapes so a
// literal backslash-u0000 stays literal; never rewrite ACP checkpoint JSONL.
// Apart from the NUL escape, all bytes (including number precision) are kept.
func postgresMessageJSON(raw []byte) []byte {
	var out []byte
	for i := 0; i < len(raw); i++ {
		if raw[i] != '\\' {
			continue
		}
		if bytes.HasPrefix(raw[i:], []byte(`\u0000`)) {
			if out == nil {
				out = bytes.Clone(raw)
			}
			copy(out[i:i+6], `\u2400`)
			i += 5
		} else {
			i++ // Skip an escaped backslash or quote as one JSON escape.
		}
	}
	if out == nil {
		return raw
	}
	return out
}
