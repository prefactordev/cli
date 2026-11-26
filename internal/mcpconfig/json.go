package mcpconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

// jsonFormat describes a client whose servers live in a JSON file.
type jsonFormat struct {
	// serversKey is the top-level key that holds the servers.
	serversKey string
	// entry builds the entry's members in the order they are written.
	entry func(Entry) []member
	// input is VS Code's prompt for the token, kept in the top-level inputs
	// array.
	input []member
}

func jsonFormatFor(client Client) (jsonFormat, error) {
	switch client {
	case Cursor:
		return jsonFormat{
			serversKey: "mcpServers",
			entry: func(e Entry) []member {
				return withToken(e, []member{str("url", e.URL)}, "Bearer ${env:"+TokenEnvVar+"}")
			},
		}, nil
	case ClaudeCode:
		return jsonFormat{
			serversKey: "mcpServers",
			entry: func(e Entry) []member {
				return withToken(e, []member{str("type", "http"), str("url", e.URL)}, "Bearer ${"+TokenEnvVar+"}")
			},
		}, nil
	case VSCode:
		return jsonFormat{
			serversKey: "servers",
			entry: func(e Entry) []member {
				return withToken(e, []member{str("type", "http"), str("url", e.URL)}, "Bearer ${input:"+VSCodeInputID+"}")
			},
			input: []member{
				str("type", "promptString"),
				str("id", VSCodeInputID),
				str("description", "Prefactor read-only API token (run: prefactor mcp token)"),
				{"password", json.RawMessage("true")},
			},
		}, nil
	}
	return jsonFormat{}, unknownClient(client)
}

// apply returns the file with entry set, or removed when entry is nil.
// Members the edit does not touch keep their order and their JSON as
// written, apart from indentation.
func (f jsonFormat) apply(old []byte, entry *Entry) ([]byte, error) {
	root := object{}
	if len(bytes.TrimSpace(old)) > 0 {
		if err := root.parse(old); err != nil {
			return nil, fmt.Errorf("it is not a JSON object that can be edited safely (%w); add the entry by hand, or use prefactor mcp config to print it", err)
		}
	}

	servers := object{}
	if raw, ok := root.get(f.serversKey); ok && !isNull(raw) {
		if err := servers.parse(raw); err != nil {
			return nil, fmt.Errorf("%q is not a JSON object", f.serversKey)
		}
	} else if entry == nil {
		return old, nil
	}

	if entry == nil {
		servers.remove(ServerName)
	} else {
		servers.set(ServerName, object(f.entry(*entry)).encode())
	}
	root.set(f.serversKey, servers.encode())

	if f.input != nil {
		if err := f.applyInput(&root, entry != nil && entry.Token); err != nil {
			return nil, err
		}
	}

	// A file whose content would not change is left exactly as it is,
	// whatever its layout.
	encoded := root.encode()
	var was, now bytes.Buffer
	if len(bytes.TrimSpace(old)) > 0 && json.Compact(&was, old) == nil && json.Compact(&now, encoded) == nil && bytes.Equal(was.Bytes(), now.Bytes()) {
		return old, nil
	}

	var out bytes.Buffer
	if err := json.Indent(&out, encoded, "", detectIndent(old)); err != nil {
		return nil, err
	}
	out.WriteByte('\n')
	return out.Bytes(), nil
}

// detectIndent returns the indentation of the first indented line of a
// file, so an edited file keeps its style, or two spaces.
func detectIndent(raw []byte) string {
	for _, line := range bytes.Split(raw, []byte("\n"))[1:] {
		trimmed := bytes.TrimLeft(line, " \t")
		if indent := line[:len(line)-len(trimmed)]; len(indent) > 0 && len(trimmed) > 0 {
			return string(indent)
		}
	}
	return "  "
}

// applyInput removes this package's input from the inputs array and, when
// want is set, adds it back at the end. An inputs array that is missing and
// not wanted stays missing.
func (f jsonFormat) applyInput(root *object, want bool) error {
	var inputs []json.RawMessage
	raw, ok := root.get("inputs")
	if ok && !isNull(raw) {
		if err := json.Unmarshal(raw, &inputs); err != nil {
			return errors.New(`"inputs" is not a JSON array`)
		}
	} else if !want {
		return nil
	}

	kept := inputs[:0:0]
	for _, input := range inputs {
		var probe struct {
			ID string `json:"id"`
		}
		if json.Unmarshal(input, &probe) == nil && probe.ID == VSCodeInputID {
			continue
		}
		kept = append(kept, input)
	}
	if want {
		kept = append(kept, object(f.input).encode())
	}

	var b bytes.Buffer
	b.WriteByte('[')
	for i, input := range kept {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(input)
	}
	b.WriteByte(']')
	root.set("inputs", b.Bytes())
	return nil
}

// member is one key and its JSON value.
type member struct {
	key   string
	value json.RawMessage
}

// object is a JSON object as an ordered list of members, so editing one
// member leaves the others as they were.
type object []member

func (o *object) parse(raw []byte) error {
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	tok, err := dec.Token()
	if err != nil {
		return err
	}
	if delim, ok := tok.(json.Delim); !ok || delim != '{' {
		return errors.New("the top level is not an object")
	}
	*o = (*o)[:0]
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			return err
		}
		key, _ := tok.(string)
		var value json.RawMessage
		if err := dec.Decode(&value); err != nil {
			return err
		}
		*o = append(*o, member{key, value})
	}
	if _, err := dec.Token(); err != nil {
		return err
	}
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return errors.New("there is more after the object")
	}
	return nil
}

func (o object) get(key string) (json.RawMessage, bool) {
	for _, m := range o {
		if m.key == key {
			return m.value, true
		}
	}
	return nil, false
}

// set replaces the first member called key and drops any repeats of it, or
// adds the member at the end.
func (o *object) set(key string, value json.RawMessage) {
	found := false
	kept := (*o)[:0]
	for _, m := range *o {
		if m.key == key {
			if found {
				continue
			}
			m.value, found = value, true
		}
		kept = append(kept, m)
	}
	if !found {
		kept = append(kept, member{key, value})
	}
	*o = kept
}

func (o *object) remove(key string) {
	kept := (*o)[:0]
	for _, m := range *o {
		if m.key != key {
			kept = append(kept, m)
		}
	}
	*o = kept
}

func (o object) encode() json.RawMessage {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, m := range o {
		if i > 0 {
			b.WriteByte(',')
		}
		b.Write(encodeString(m.key))
		b.WriteByte(':')
		b.Write(m.value)
	}
	b.WriteByte('}')
	return b.Bytes()
}

func isNull(raw json.RawMessage) bool {
	return string(bytes.TrimSpace(raw)) == "null"
}

func encodeString(s string) []byte {
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	_ = enc.Encode(s)
	return bytes.TrimSuffix(b.Bytes(), []byte("\n"))
}

func str(key, value string) member {
	return member{key, encodeString(value)}
}

// withToken adds an Authorization header with the given value to members
// when the entry sends the read-only token.
func withToken(e Entry, members []member, authorization string) []member {
	if !e.Token {
		return members
	}
	return append(members, member{"headers", object{str("Authorization", authorization)}.encode()})
}
