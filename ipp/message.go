package ipp

import (
	"errors"
	"fmt"
)

// Sentinel errors. Detailed errors wrap them and are reachable via errors.Is.
var (
	// ErrMalformed is returned when a message cannot be decoded.
	ErrMalformed = errors.New("ipp: malformed message")
	// ErrInvalid is returned when a message or argument cannot be encoded
	// or is otherwise invalid.
	ErrInvalid = errors.New("ipp: invalid argument")
)

func invalidf(format string, args ...any) error {
	return fmt.Errorf("%w: "+format, append([]any{ErrInvalid}, args...)...)
}

// Version is an IPP protocol version: major number in the high byte, minor
// number in the low byte.
type Version uint16

// Protocol versions.
const (
	Version10 Version = 0x0100
	Version11 Version = 0x0101
	Version20 Version = 0x0200
	Version21 Version = 0x0201
	Version22 Version = 0x0202
)

// Major returns the major version number.
func (v Version) Major() int { return int(v >> 8) }

// Minor returns the minor version number.
func (v Version) Minor() int { return int(v & 0xff) }

// String returns e.g. "2.0".
func (v Version) String() string { return fmt.Sprintf("%d.%d", v.Major(), v.Minor()) }

// Message is an IPP request or response (RFC 8010 §3.1.1) without its
// document data. Document data follows the encoded message on the wire.
type Message struct {
	Version Version
	// Code is the operation-id of a request or the status-code of a
	// response; see [Message.Operation] and [Message.Status].
	Code      uint16
	RequestID int32
	Groups    []Group
}

// Group is an attribute group. Tag is a delimiter tag such as
// [TagOperationGroup] or [TagPrinterGroup]. A message may contain several
// groups with the same tag (e.g. one job group per job).
type Group struct {
	Tag   Tag
	Attrs Attributes
}

// Attributes is an ordered list of attributes.
type Attributes []Attribute

// Attribute is a named attribute with one or more values. Values of a
// multi-valued attribute may have different tags (e.g. a keyword and a
// name).
type Attribute struct {
	Name   string
	Values []Value
}

// NewRequest returns an IPP/2.0 request for op with the operation group
// holding the mandatory attributes-charset (utf-8) and
// attributes-natural-language (en).
func NewRequest(op Operation, requestID int32) *Message {
	return newMessage(uint16(op), requestID)
}

// NewResponse returns an IPP/2.0 response with status st and the operation
// group holding attributes-charset (utf-8) and attributes-natural-language
// (en).
func NewResponse(st Status, requestID int32) *Message {
	return newMessage(uint16(st), requestID)
}

func newMessage(code uint16, requestID int32) *Message {
	return &Message{
		Version:   Version20,
		Code:      code,
		RequestID: requestID,
		Groups: []Group{{Tag: TagOperationGroup, Attrs: Attributes{
			{Name: "attributes-charset", Values: []Value{Charset("utf-8")}},
			{Name: "attributes-natural-language", Values: []Value{NaturalLanguage("en")}},
		}}},
	}
}

// Operation returns Code as an operation-id.
func (m *Message) Operation() Operation { return Operation(m.Code) }

// Status returns Code as a status-code.
func (m *Message) Status() Status { return Status(m.Code) }

// Group returns the first group with the given tag, or nil.
func (m *Message) Group(tag Tag) *Group {
	for i := range m.Groups {
		if m.Groups[i].Tag == tag {
			return &m.Groups[i]
		}
	}
	return nil
}

// GroupsByTag returns all groups with the given tag, in message order.
func (m *Message) GroupsByTag(tag Tag) []Group {
	var gs []Group
	for _, g := range m.Groups {
		if g.Tag == tag {
			gs = append(gs, g)
		}
	}
	return gs
}

// AddGroup appends an empty group with the given tag and returns it. The
// pointer is valid until the next change to m.Groups.
func (m *Message) AddGroup(tag Tag) *Group {
	m.Groups = append(m.Groups, Group{Tag: tag})
	return &m.Groups[len(m.Groups)-1]
}

// operationGroup returns the operation group, adding it if missing.
func (m *Message) operationGroup() *Group {
	if g := m.Group(TagOperationGroup); g != nil {
		return g
	}
	return m.AddGroup(TagOperationGroup)
}

// Unsupported returns the attributes of all unsupported-attributes groups:
// attributes or values the recipient ignored or substituted.
func (m *Message) Unsupported() Attributes {
	var as Attributes
	for _, g := range m.GroupsByTag(TagUnsupportedGroup) {
		as = append(as, g.Attrs...)
	}
	return as
}

// StatusMessage returns the status-message operation attribute, if any.
func (m *Message) StatusMessage() string {
	if g := m.Group(TagOperationGroup); g != nil {
		if a, ok := g.Attrs.Get("status-message"); ok {
			return a.String()
		}
	}
	return ""
}

// Get returns the first attribute with the given name.
func (as Attributes) Get(name string) (Attribute, bool) {
	for _, a := range as {
		if a.Name == name {
			return a, true
		}
	}
	return Attribute{}, false
}

// Add appends an attribute with the given values.
func (as *Attributes) Add(name string, values ...Value) {
	*as = append(*as, Attribute{Name: name, Values: values})
}

// Set replaces the values of the first attribute with the given name, or
// appends the attribute if there is none.
func (as *Attributes) Set(name string, values ...Value) {
	for i := range *as {
		if (*as)[i].Name == name {
			(*as)[i].Values = values
			return
		}
	}
	as.Add(name, values...)
}

// String returns the string form of the first value, or "" if there is
// none.
func (a Attribute) String() string {
	if len(a.Values) == 0 {
		return ""
	}
	return valueString(a.Values[0])
}

// Strings returns the string form of all values.
func (a Attribute) Strings() []string {
	s := make([]string, len(a.Values))
	for i, v := range a.Values {
		s[i] = valueString(v)
	}
	return s
}

// Int returns the first value if it is an [Integer] or [Enum].
func (a Attribute) Int() (int, bool) {
	if len(a.Values) == 0 {
		return 0, false
	}
	switch v := a.Values[0].(type) {
	case Integer:
		return int(v), true
	case Enum:
		return int(v), true
	}
	return 0, false
}

// Bool returns the first value if it is a [Boolean].
func (a Attribute) Bool() (value, ok bool) {
	if len(a.Values) == 0 {
		return false, false
	}
	b, ok := a.Values[0].(Boolean)
	return bool(b), ok
}
