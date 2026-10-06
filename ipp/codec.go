package ipp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	// MaxCollectionDepth is the maximum nesting depth of collections the
	// codec encodes and decodes. media-col-database needs 2.
	MaxCollectionDepth = 16

	// MaxMessageSize bounds the attribute section [Decode] reads from a
	// stream. Each decoded group, attribute and value is charged an extra
	// fixed overhead against it, so it also bounds memory use.
	MaxMessageSize = 16 << 20

	elemOverhead = 48 // budget charged per decoded element in stream mode

	maxLength = 0xFFFF // largest name-length / value-length
)

// streamLimit is the attribute section limit of [Decode]; tests lower it.
var streamLimit int64 = MaxMessageSize

// Encode writes the binary encoding of m (header, attribute groups and the
// end-of-attributes tag) to w. Document data, if any, is written by the
// caller after it.
func (m *Message) Encode(w io.Writer) error {
	b, err := m.MarshalBinary()
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// MarshalBinary returns the binary encoding of m. It fails with an error
// wrapping [ErrInvalid] if m cannot be represented, e.g. because of an
// empty attribute name, an attribute without values, an over-long value or
// an over-deep collection.
func (m *Message) MarshalBinary() ([]byte, error) {
	b := make([]byte, 8, 512)
	binary.BigEndian.PutUint16(b[0:], uint16(m.Version))
	binary.BigEndian.PutUint16(b[2:], m.Code)
	binary.BigEndian.PutUint32(b[4:], uint32(m.RequestID))
	for _, g := range m.Groups {
		if g.Tag == 0 || g.Tag == TagEnd || !g.Tag.IsDelimiter() {
			return nil, invalidf("group tag %s is not a group delimiter", g.Tag)
		}
		b = append(b, byte(g.Tag))
		for _, a := range g.Attrs {
			if a.Name == "" {
				return nil, invalidf("attribute with empty name in %s", g.Tag)
			}
			if len(a.Values) == 0 {
				return nil, invalidf("attribute %q has no values", a.Name)
			}
			name := a.Name
			for _, v := range a.Values {
				var err error
				if b, err = appendValue(b, name, v, 0); err != nil {
					return nil, fmt.Errorf("attribute %q: %w", a.Name, err)
				}
				name = "" // additional values
			}
		}
	}
	return append(b, byte(TagEnd)), nil
}

func appendString(b []byte, s string) ([]byte, error) {
	if len(s) > maxLength {
		return b, invalidf("string of %d bytes exceeds %d", len(s), maxLength)
	}
	b = binary.BigEndian.AppendUint16(b, uint16(len(s)))
	return append(b, s...), nil
}

// appendValue appends one value with the given (possibly empty) name.
// depth is the collection nesting depth of the value.
func appendValue(b []byte, name string, v Value, depth int) ([]byte, error) {
	if v == nil {
		return b, invalidf("nil value")
	}
	tag := v.Tag()
	b = append(b, byte(tag))
	b, err := appendString(b, name)
	if err != nil {
		return b, err
	}
	switch v := v.(type) {
	case Integer:
		b = binary.BigEndian.AppendUint16(b, 4)
		return binary.BigEndian.AppendUint32(b, uint32(v)), nil
	case Enum:
		b = binary.BigEndian.AppendUint16(b, 4)
		return binary.BigEndian.AppendUint32(b, uint32(v)), nil
	case Boolean:
		x := byte(0)
		if v {
			x = 1
		}
		return append(b, 0, 1, x), nil
	case OctetString:
		return appendString(b, string(v))
	case Text:
		return appendString(b, string(v))
	case Name:
		return appendString(b, string(v))
	case Keyword:
		return appendString(b, string(v))
	case URI:
		return appendString(b, string(v))
	case URIScheme:
		return appendString(b, string(v))
	case Charset:
		return appendString(b, string(v))
	case NaturalLanguage:
		return appendString(b, string(v))
	case MimeMediaType:
		return appendString(b, string(v))
	case TextWithLanguage:
		return appendWithLanguage(b, v.Lang, v.Text)
	case NameWithLanguage:
		return appendWithLanguage(b, v.Lang, v.Name)
	case DateTime:
		return append(b, 0, 11, byte(v.Year>>8), byte(v.Year), v.Month, v.Day,
			v.Hour, v.Minute, v.Second, v.Deciseconds,
			v.UTCDirection, v.UTCHours, v.UTCMinutes), nil
	case Resolution:
		b = append(b, 0, 9)
		b = binary.BigEndian.AppendUint32(b, uint32(v.X))
		b = binary.BigEndian.AppendUint32(b, uint32(v.Y))
		return append(b, byte(v.Units)), nil
	case Range:
		b = append(b, 0, 8)
		b = binary.BigEndian.AppendUint32(b, uint32(v.Lower))
		return binary.BigEndian.AppendUint32(b, uint32(v.Upper)), nil
	case Collection:
		return appendCollection(b, v, depth+1)
	case OutOfBand:
		if !tag.IsOutOfBand() {
			return b, invalidf("out-of-band value with tag %s", tag)
		}
		return append(b, 0, 0), nil
	case Raw:
		if tag.IsDelimiter() || tag.IsOutOfBand() || tag.known() {
			return b, invalidf("raw value with reserved tag %s", tag)
		}
		return appendString(b, string(v.Data))
	}
	return b, invalidf("unsupported value type %T", v)
}

func appendWithLanguage(b []byte, lang, s string) ([]byte, error) {
	n := 4 + len(lang) + len(s)
	if n > maxLength {
		return b, invalidf("value of %d bytes exceeds %d", n, maxLength)
	}
	b = binary.BigEndian.AppendUint16(b, uint16(n))
	b = binary.BigEndian.AppendUint16(b, uint16(len(lang)))
	b = append(b, lang...)
	b = binary.BigEndian.AppendUint16(b, uint16(len(s)))
	return append(b, s...), nil
}

// appendCollection appends the collection members and the endCollection
// value; the begCollection tag and name have already been written.
func appendCollection(b []byte, c Collection, depth int) ([]byte, error) {
	if depth > MaxCollectionDepth {
		return b, invalidf("collection nesting exceeds %d", MaxCollectionDepth)
	}
	b = append(b, 0, 0) // begCollection value-length
	for _, a := range c {
		if len(a.Values) == 0 {
			return b, invalidf("collection member %q has no values", a.Name)
		}
		b = append(b, byte(TagMemberName), 0, 0)
		var err error
		if b, err = appendString(b, a.Name); err != nil {
			return b, err
		}
		for _, v := range a.Values {
			if b, err = appendValue(b, "", v, depth); err != nil {
				return b, fmt.Errorf("member %q: %w", a.Name, err)
			}
		}
	}
	return append(b, byte(TagEndCollection), 0, 0, 0, 0), nil
}

// Decode reads one message from r: the header and all attribute groups up
// to and including the end-of-attributes tag. It does not read beyond that,
// so any document data can be read from r afterwards. Errors wrap
// [ErrMalformed]; an attribute section larger than [MaxMessageSize] is
// rejected.
func Decode(r io.Reader) (*Message, error) {
	d := &decoder{r: r, left: streamLimit}
	return d.message()
}

// UnmarshalBinary decodes data, which must contain exactly one message
// without document data, into m. Use [Decode] for messages followed by
// document data.
func (m *Message) UnmarshalBinary(data []byte) error {
	d := &decoder{data: data, left: int64(len(data))}
	msg, err := d.message()
	if err != nil {
		return err
	}
	if d.left != 0 {
		return fmt.Errorf("%w: %d bytes of trailing data", ErrMalformed, d.left)
	}
	*m = *msg
	return nil
}

// decoder reads from either an in-memory buffer (data) or a stream (r).
// left bounds the bytes that may still be consumed, so no length field can
// cause an allocation beyond the remaining input.
type decoder struct {
	data []byte
	r    io.Reader
	off  int64
	left int64
}

func (d *decoder) errorf(format string, args ...any) error {
	return fmt.Errorf("%w: offset %d: "+format, append([]any{ErrMalformed, d.off}, args...)...)
}

// next returns the next n bytes. In buffer mode the result aliases the
// input and must be copied before it is retained.
func (d *decoder) next(n int) ([]byte, error) {
	if int64(n) > d.left {
		if d.data != nil || d.r == nil {
			return nil, d.errorf("length %d exceeds remaining input of %d bytes: %w", n, d.left, io.ErrUnexpectedEOF)
		}
		return nil, d.errorf("message exceeds %d bytes", streamLimit)
	}
	var b []byte
	if d.r == nil {
		b = d.data[d.off : d.off+int64(n) : d.off+int64(n)]
	} else {
		b = make([]byte, n)
		if _, err := io.ReadFull(d.r, b); err != nil {
			if errors.Is(err, io.EOF) {
				err = io.ErrUnexpectedEOF
			}
			return nil, d.errorf("%w", err)
		}
	}
	d.off += int64(n)
	d.left -= int64(n)
	return b, nil
}

// grow charges the memory overhead of one decoded element against the
// stream budget. In buffer mode the input bounds the element count.
func (d *decoder) grow() error {
	if d.r == nil {
		return nil
	}
	if d.left < elemOverhead {
		return d.errorf("message exceeds %d bytes", streamLimit)
	}
	d.left -= elemOverhead
	return nil
}

func (d *decoder) byte() (byte, error) {
	b, err := d.next(1)
	if err != nil {
		return 0, err
	}
	return b[0], nil
}

// field reads a 2-byte length followed by that many bytes.
func (d *decoder) field() ([]byte, error) {
	b, err := d.next(2)
	if err != nil {
		return nil, err
	}
	return d.next(int(binary.BigEndian.Uint16(b)))
}

func (d *decoder) message() (*Message, error) {
	h, err := d.next(8)
	if err != nil {
		return nil, err
	}
	m := &Message{
		Version:   Version(binary.BigEndian.Uint16(h[0:])),
		Code:      binary.BigEndian.Uint16(h[2:]),
		RequestID: int32(binary.BigEndian.Uint32(h[4:])),
	}
	for {
		t, err := d.byte()
		if err != nil {
			return nil, err
		}
		tag := Tag(t)
		switch {
		case tag == TagEnd:
			return m, nil
		case tag == 0:
			return nil, d.errorf("reserved delimiter tag 0x00")
		case tag.IsDelimiter():
			if err := d.grow(); err != nil {
				return nil, err
			}
			m.Groups = append(m.Groups, Group{Tag: tag})
			continue
		}
		if len(m.Groups) == 0 {
			return nil, d.errorf("attribute before the first group")
		}
		g := &m.Groups[len(m.Groups)-1]
		name, err := d.field()
		if err != nil {
			return nil, err
		}
		if len(name) == 0 && len(g.Attrs) == 0 {
			return nil, d.errorf("additional value without attribute")
		}
		v, err := d.value(tag, 0)
		if err != nil {
			return nil, err
		}
		if err := d.grow(); err != nil {
			return nil, err
		}
		if len(name) == 0 {
			last := &g.Attrs[len(g.Attrs)-1]
			last.Values = append(last.Values, v)
		} else {
			g.Attrs = append(g.Attrs, Attribute{Name: string(name), Values: []Value{v}})
		}
	}
}

// value reads the value-length and value for tag. depth is the collection
// nesting depth of the value.
func (d *decoder) value(tag Tag, depth int) (Value, error) {
	b, err := d.field()
	if err != nil {
		return nil, err
	}
	fixed := func(n int) error {
		if len(b) != n {
			return d.errorf("%s value of %d bytes, want %d", tag, len(b), n)
		}
		return nil
	}
	switch {
	case tag.IsOutOfBand():
		return OutOfBand(tag), nil // RFC 8010: value is ignored
	case tag == TagBeginCollection:
		return d.collection(depth + 1) // begCollection value is ignored
	case tag == TagEndCollection, tag == TagMemberName:
		return nil, d.errorf("%s outside of a collection", tag)
	case !tag.known():
		return Raw{ValueTag: tag, Data: clone(b)}, nil
	}
	switch tag {
	case TagInteger, TagEnum:
		if err := fixed(4); err != nil {
			return nil, err
		}
		n := int32(binary.BigEndian.Uint32(b))
		if tag == TagEnum {
			return Enum(n), nil
		}
		return Integer(n), nil
	case TagBoolean:
		if err := fixed(1); err != nil {
			return nil, err
		}
		return Boolean(b[0] != 0), nil
	case TagDateTime:
		if err := fixed(11); err != nil {
			return nil, err
		}
		return DateTime{
			Year: binary.BigEndian.Uint16(b), Month: b[2], Day: b[3],
			Hour: b[4], Minute: b[5], Second: b[6], Deciseconds: b[7],
			UTCDirection: b[8], UTCHours: b[9], UTCMinutes: b[10],
		}, nil
	case TagResolution:
		if err := fixed(9); err != nil {
			return nil, err
		}
		return Resolution{
			X:     int32(binary.BigEndian.Uint32(b)),
			Y:     int32(binary.BigEndian.Uint32(b[4:])),
			Units: Units(b[8]),
		}, nil
	case TagRange:
		if err := fixed(8); err != nil {
			return nil, err
		}
		return Range{
			Lower: int32(binary.BigEndian.Uint32(b)),
			Upper: int32(binary.BigEndian.Uint32(b[4:])),
		}, nil
	case TagTextWithLanguage, TagNameWithLanguage:
		lang, s, ok := splitWithLanguage(b)
		if !ok {
			return nil, d.errorf("malformed %s value", tag)
		}
		if tag == TagTextWithLanguage {
			return TextWithLanguage{Lang: lang, Text: s}, nil
		}
		return NameWithLanguage{Lang: lang, Name: s}, nil
	case TagOctetString:
		return OctetString(clone(b)), nil
	case TagText:
		return Text(b), nil
	case TagName:
		return Name(b), nil
	case TagKeyword:
		return Keyword(b), nil
	case TagURI:
		return URI(b), nil
	case TagURIScheme:
		return URIScheme(b), nil
	case TagCharset:
		return Charset(b), nil
	case TagNaturalLanguage:
		return NaturalLanguage(b), nil
	default: // TagMimeMediaType
		return MimeMediaType(b), nil
	}
}

func splitWithLanguage(b []byte) (lang, s string, ok bool) {
	if len(b) < 2 {
		return "", "", false
	}
	n := int(binary.BigEndian.Uint16(b))
	b = b[2:]
	if len(b) < n+2 {
		return "", "", false
	}
	lang, b = string(b[:n]), b[n:]
	n = int(binary.BigEndian.Uint16(b))
	b = b[2:]
	if len(b) != n {
		return "", "", false
	}
	return lang, string(b), true
}

// collection reads collection members up to and including endCollection.
func (d *decoder) collection(depth int) (Collection, error) {
	if depth > MaxCollectionDepth {
		return nil, d.errorf("collection nesting exceeds %d", MaxCollectionDepth)
	}
	var c Collection
	for {
		t, err := d.byte()
		if err != nil {
			return nil, err
		}
		tag := Tag(t)
		name, err := d.field()
		if err != nil {
			return nil, err
		}
		if len(name) != 0 {
			return nil, d.errorf("named %s value inside a collection", tag)
		}
		if (tag == TagEndCollection || tag == TagMemberName) && len(c) > 0 && len(c[len(c)-1].Values) == 0 {
			return nil, d.errorf("collection member %q has no values", c[len(c)-1].Name)
		}
		switch {
		case tag == TagEndCollection:
			if _, err := d.field(); err != nil {
				return nil, err
			}
			return c, nil
		case tag == TagMemberName:
			mn, err := d.field()
			if err != nil {
				return nil, err
			}
			if err := d.grow(); err != nil {
				return nil, err
			}
			c = append(c, Attribute{Name: string(mn)})
		case tag.IsDelimiter():
			return nil, d.errorf("delimiter %s inside a collection", tag)
		case len(c) == 0:
			return nil, d.errorf("collection value without member name")
		default:
			v, err := d.value(tag, depth)
			if err != nil {
				return nil, err
			}
			if err := d.grow(); err != nil {
				return nil, err
			}
			last := &c[len(c)-1]
			last.Values = append(last.Values, v)
		}
	}
}

func clone(b []byte) []byte {
	if len(b) == 0 {
		return nil
	}
	return bytes.Clone(b)
}
