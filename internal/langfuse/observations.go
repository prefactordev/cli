package langfuse

import (
	"bytes"

	"context"
	"encoding/json"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
)

// ObservationFields are the field groups an import reads. Prefactor needs
// core, basic, time, io, metadata, model and usage; trace_context adds
// traceName, tags and release, which say which agent a trace belongs to.
const ObservationFields = "core,basic,time,io,metadata,model,usage,trace_context"

// processMetadataPrefix starts the metadata keys that OpenTelemetry fills
// with details of the process that sent a trace, such as its command line
// and owner. They are dropped before upload.
const processMetadataPrefix = "resourceAttributes.process."

// Observation is one observation as the Observations API v2 returns it.
// Values stay as raw JSON, so they are uploaded unchanged.
type Observation map[string]json.RawMessage

// Text returns a string field, or "" when it is missing, null or not a
// string.
func (o Observation) Text(key string) string {
	var s string
	if raw, ok := o[key]; ok && json.Unmarshal(raw, &s) == nil {
		return s
	}
	return ""
}

// StartTime is when the observation started, or the zero time when it
// cannot be read.
func (o Observation) StartTime() time.Time {
	t, err := time.Parse(time.RFC3339Nano, o.Text("startTime"))
	if err != nil {
		return time.Time{}
	}
	return t
}

// IsRoot reports whether the observation is its trace's root: Langfuse
// marks it with isRootObservation, and older data has no parent.
func (o Observation) IsRoot() bool {
	if root, ok := o.rootMark(); ok {
		return root
	}
	return o.Text("parentObservationId") == ""
}

// rootMark is isRootObservation, and false when Langfuse did not send it.
// Older data without it can have several observations with no parent.
func (o Observation) rootMark() (root, ok bool) {
	raw, found := o["isRootObservation"]
	if !found {
		return false, false
	}
	return root, json.Unmarshal(raw, &root) == nil
}

// WithoutProcessMetadata returns the observation without the
// resourceAttributes.process.* metadata keys, which describe the machine
// that sent the trace rather than the run. Other fields are not changed.
func (o Observation) WithoutProcessMetadata() Observation {
	raw, ok := o["metadata"]
	if !ok {
		return o
	}
	var metadata map[string]json.RawMessage
	if json.Unmarshal(raw, &metadata) != nil || metadata == nil {
		return o
	}
	changed := false
	for key := range metadata {
		if strings.HasPrefix(key, processMetadataPrefix) {
			delete(metadata, key)
			changed = true
		}
	}
	if !changed {
		return o
	}
	encoded, err := marshalRaw(metadata)
	if err != nil {
		return o
	}
	out := make(Observation, len(o))
	for key, value := range o {
		out[key] = value
	}
	out["metadata"] = encoded
	return out
}

// marshalRaw encodes value without escaping <, > and &, so text in the
// metadata reaches Prefactor as Langfuse sent it.
func marshalRaw(value any) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(value); err != nil {
		return nil, err
	}
	return json.RawMessage(bytes.TrimRight(buf.Bytes(), "\n")), nil
}

// ObservationQuery selects observations. Zero times leave that end of the
// window open.
type ObservationQuery struct {
	// TraceNames keeps observations of traces with one of these names.
	TraceNames []string
	From, To   time.Time
	Limit      int
	Cursor     string
}

// ObservationPage is one page of observations, newest first. Cursor is
// empty on the last page.
type ObservationPage struct {
	Observations []Observation
	Cursor       string
}

// Observations reads one page of observations with every field group an
// import needs.
func (c *Client) Observations(ctx context.Context, q ObservationQuery) (ObservationPage, error) {
	query := url.Values{"fields": {ObservationFields}}
	if q.Limit > 0 {
		query.Set("limit", strconv.Itoa(q.Limit))
	}
	if !q.From.IsZero() {
		query.Set("fromStartTime", Timestamp(q.From))
	}
	if !q.To.IsZero() {
		query.Set("toStartTime", Timestamp(q.To))
	}
	if q.Cursor != "" {
		query.Set("cursor", q.Cursor)
	}
	if len(q.TraceNames) > 0 {
		filter, err := json.Marshal([]map[string]any{{
			"type": "stringOptions", "column": "traceName", "operator": "any of", "value": q.TraceNames,
		}})
		if err != nil {
			return ObservationPage{}, err
		}
		query.Set("filter", string(filter))
	}

	var response struct {
		Data []Observation `json:"data"`
		Meta struct {
			Cursor *string `json:"cursor"`
		} `json:"meta"`
	}
	if err := c.get(ctx, "/api/public/v2/observations", query, &response); err != nil {
		return ObservationPage{}, err
	}
	page := ObservationPage{Observations: response.Data}
	if response.Meta.Cursor != nil {
		page.Cursor = *response.Meta.Cursor
	}
	return page, nil
}

// Trace is a Langfuse trace: its ID and its observations.
type Trace struct {
	ID           string
	Observations []Observation
}

// root is the trace's earliest root observation, or its earliest
// observation when none is a root.
func (t Trace) root() Observation {
	var root, earliest Observation
	for _, o := range t.Observations {
		if o.IsRoot() && (root == nil || o.StartTime().Before(root.StartTime())) {
			root = o
		}
		if earliest == nil || o.StartTime().Before(earliest.StartTime()) {
			earliest = o
		}
	}
	if root != nil {
		return root
	}
	return earliest
}

// TraceName is the Langfuse trace name.
func (t Trace) TraceName() string {
	if root := t.root(); root != nil {
		return root.Text("traceName")
	}
	return ""
}

// uploadTrace is a trace as Prefactor's imports/add_traces action takes
// it, with Langfuse's field names.
type uploadTrace struct {
	ID           string          `json:"id"`
	Name         string          `json:"name"`
	Environment  json.RawMessage `json:"environment,omitempty"`
	Release      json.RawMessage `json:"release,omitempty"`
	Timestamp    json.RawMessage `json:"timestamp,omitempty"`
	UserID       json.RawMessage `json:"userId,omitempty"`
	SessionID    json.RawMessage `json:"sessionId,omitempty"`
	Tags         json.RawMessage `json:"tags,omitempty"`
	Observations []Observation   `json:"observations"`
}

// Encode returns the trace as JSON for upload. Its name is the trace
// name, which Prefactor uses as the agent's name. The trace fields come
// from the root observation, where Langfuse copies them; observations are
// oldest first, without process metadata.
func (t Trace) Encode() ([]byte, error) {
	upload := uploadTrace{ID: t.ID, Name: t.TraceName(), Observations: make([]Observation, len(t.Observations))}
	if root := t.root(); root != nil {
		upload.Environment = root["environment"]
		upload.Release = root["release"]
		upload.Timestamp = root["startTime"]
		upload.UserID = root["userId"]
		upload.SessionID = root["sessionId"]
		upload.Tags = root["tags"]
	}
	for i, o := range t.Observations {
		upload.Observations[i] = o.WithoutProcessMetadata()
	}
	slices.SortStableFunc(upload.Observations, func(a, b Observation) int {
		return a.StartTime().Compare(b.StartTime())
	})
	return marshalRaw(upload)
}

// Assembler groups observations read newest first into traces, and says
// when each trace is complete.
//
// The observations of a trace start at or after its root, give or take
// clock differences between the services that sent them. So once a page
// ends with an observation more than rootMargin older than a trace's
// root, the trace has no more observations to come. A trace whose root
// Langfuse does not mark with isRootObservation, as in older data, can
// have several observations without a parent, so it is complete only at
// Finish. A trace whose root is not seen by the end started before the
// window, so it is not one of the window's runs and is left out.
type Assembler struct {
	pending map[string]*pendingTrace
	// order is the order traces were first seen, so results are stable.
	order []string
	// done holds the IDs of traces already returned.
	done map[string]bool
	// late counts observations that arrived after their trace was
	// returned.
	late int
}

// rootMargin is how much earlier than its root an observation of the same
// trace may start.
const rootMargin = 5 * time.Minute

type pendingTrace struct {
	trace     Trace
	ids       map[string]bool
	hasRoot   bool
	rootStart time.Time
	// unmarkedRoot is set when a root was found only by its missing
	// parent.
	unmarkedRoot bool
}

// NewAssembler returns an empty Assembler.
func NewAssembler() *Assembler {
	return &Assembler{pending: map[string]*pendingTrace{}, done: map[string]bool{}}
}

// Add takes the next page of observations and returns the traces it
// completes, in the order they were first seen.
func (a *Assembler) Add(observations []Observation) []Trace {
	var oldest time.Time
	for _, o := range observations {
		traceID, id := o.Text("traceId"), o.Text("id")
		if traceID == "" || id == "" {
			continue
		}
		if a.done[traceID] {
			a.late++
			continue
		}
		p := a.pending[traceID]
		if p == nil {
			p = &pendingTrace{trace: Trace{ID: traceID}, ids: map[string]bool{}}
			a.pending[traceID] = p
			a.order = append(a.order, traceID)
		}
		if p.ids[id] {
			continue
		}
		p.ids[id] = true
		p.trace.Observations = append(p.trace.Observations, o)
		start := o.StartTime()
		if o.IsRoot() {
			if _, marked := o.rootMark(); !marked {
				p.unmarkedRoot = true
			}
			if !p.hasRoot || start.Before(p.rootStart) {
				p.hasRoot, p.rootStart = true, start
			}
		}
		if oldest.IsZero() || start.Before(oldest) {
			oldest = start
		}
	}
	if oldest.IsZero() {
		return nil
	}
	return a.take(func(p *pendingTrace) bool {
		return p.hasRoot && !p.unmarkedRoot && oldest.Before(p.rootStart.Add(-rootMargin))
	})
}

// Late is how many observations arrived after their trace was returned,
// and so were not sent with it.
func (a *Assembler) Late() int {
	return a.late
}

// Finish ends the window. It returns the traces whose root was seen, which
// are complete because nothing older is left, and drops the rest.
func (a *Assembler) Finish() []Trace {
	complete := a.take(func(p *pendingTrace) bool { return p.hasRoot })
	clear(a.pending)
	a.order = nil
	return complete
}

// take removes and returns the pending traces that ready accepts.
func (a *Assembler) take(ready func(*pendingTrace) bool) []Trace {
	var traces []Trace
	rest := a.order[:0]
	for _, id := range a.order {
		p, ok := a.pending[id]
		if !ok {
			continue
		}
		if ready(p) {
			traces = append(traces, p.trace)
			delete(a.pending, id)
			a.done[id] = true
			continue
		}
		rest = append(rest, id)
	}
	a.order = rest
	return traces
}
