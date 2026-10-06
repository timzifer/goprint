package ipp

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Value is a single attribute value. Its dynamic type determines the value
// tag on the wire:
//
//	Integer           integer
//	Enum              enum
//	Boolean           boolean
//	OctetString       octetString
//	DateTime          dateTime
//	Resolution        resolution
//	Range             rangeOfInteger
//	Collection        begCollection ... endCollection
//	TextWithLanguage  textWithLanguage
//	NameWithLanguage  nameWithLanguage
//	Text              textWithoutLanguage
//	Name              nameWithoutLanguage
//	Keyword           keyword
//	URI               uri
//	URIScheme         uriScheme
//	Charset           charset
//	NaturalLanguage   naturalLanguage
//	MimeMediaType     mimeMediaType
//	OutOfBand         unsupported, unknown, no-value, ...
//	Raw               any other value tag, kept verbatim
//
// The set of implementations is closed.
type Value interface {
	// Tag returns the value tag used to encode the value.
	Tag() Tag
	// String returns a human-readable form of the value.
	String() string

	isValue()
}

// Integer is an integer value.
type Integer int32

// Enum is an enum value, e.g. a [PrinterState] or [JobState].
type Enum int32

// Boolean is a boolean value.
type Boolean bool

// OctetString is an octetString value.
type OctetString []byte

// Text is a textWithoutLanguage value.
type Text string

// Name is a nameWithoutLanguage value.
type Name string

// Keyword is a keyword value.
type Keyword string

// URI is a uri value.
type URI string

// URIScheme is a uriScheme value.
type URIScheme string

// Charset is a charset value.
type Charset string

// NaturalLanguage is a naturalLanguage value.
type NaturalLanguage string

// MimeMediaType is a mimeMediaType value.
type MimeMediaType string

// TextWithLanguage is a textWithLanguage value.
type TextWithLanguage struct {
	Lang string
	Text string
}

// NameWithLanguage is a nameWithLanguage value.
type NameWithLanguage struct {
	Lang string
	Name string
}

// Units are the units of a [Resolution].
type Units int8

// Resolution units.
const (
	UnitsDPI  Units = 3 // dots per inch
	UnitsDPCM Units = 4 // dots per centimeter
)

// String returns "dpi", "dpcm" or the numeric value for unknown units.
func (u Units) String() string {
	switch u {
	case UnitsDPI:
		return "dpi"
	case UnitsDPCM:
		return "dpcm"
	}
	return "units(" + strconv.Itoa(int(u)) + ")"
}

// Resolution is a resolution value: cross-feed (X) and feed (Y) direction
// resolution in Units.
type Resolution struct {
	X, Y  int32
	Units Units
}

// Range is a rangeOfInteger value; both bounds are inclusive.
type Range struct {
	Lower, Upper int32
}

// DateTime is a dateTime value (RFC 2579 DateAndTime). The fields are kept
// exactly as transmitted so that decoding and re-encoding is lossless; use
// [NewDateTime] and [DateTime.Time] to convert from and to [time.Time].
type DateTime struct {
	Year        uint16
	Month       uint8 // 1-12
	Day         uint8 // 1-31
	Hour        uint8 // 0-23
	Minute      uint8 // 0-59
	Second      uint8 // 0-60
	Deciseconds uint8 // 0-9
	// UTCDirection is '+' or '-'.
	UTCDirection byte
	UTCHours     uint8
	UTCMinutes   uint8
}

// NewDateTime converts t to a DateTime, truncating to deciseconds.
func NewDateTime(t time.Time) DateTime {
	_, off := t.Zone()
	dir := byte('+')
	if off < 0 {
		dir, off = '-', -off
	}
	return DateTime{
		Year:         uint16(t.Year()),
		Month:        uint8(t.Month()),
		Day:          uint8(t.Day()),
		Hour:         uint8(t.Hour()),
		Minute:       uint8(t.Minute()),
		Second:       uint8(t.Second()),
		Deciseconds:  uint8(t.Nanosecond() / 1e8),
		UTCDirection: dir,
		UTCHours:     uint8(off / 3600),
		UTCMinutes:   uint8(off % 3600 / 60),
	}
}

// Time converts d to a [time.Time] in a fixed zone. Out-of-range fields are
// normalized as by [time.Date].
func (d DateTime) Time() time.Time {
	off := int(d.UTCHours)*3600 + int(d.UTCMinutes)*60
	if d.UTCDirection == '-' {
		off = -off
	}
	return time.Date(int(d.Year), time.Month(d.Month), int(d.Day),
		int(d.Hour), int(d.Minute), int(d.Second), int(d.Deciseconds)*1e8,
		time.FixedZone("", off))
}

// Collection is a collection value (RFC 8010 §3.1.6): an ordered list of
// member attributes, e.g. a media-col.
type Collection []Attribute

// OutOfBand is an out-of-band value. Its value is the tag itself, which must
// be in the range 0x10-0x1F; see [Unsupported], [Unknown] and [NoValue].
type OutOfBand Tag

// Common out-of-band values.
const (
	Unsupported = OutOfBand(TagUnsupportedValue)
	Unknown     = OutOfBand(TagUnknown)
	NoValue     = OutOfBand(TagNoValue)
)

// Raw is a value with a tag the codec has no dedicated type for (including
// the 0x7F extension tag). Data is kept verbatim.
type Raw struct {
	ValueTag Tag
	Data     []byte
}

// Tag implements [Value].
func (Integer) Tag() Tag { return TagInteger }

// Tag implements [Value].
func (Enum) Tag() Tag { return TagEnum }

// Tag implements [Value].
func (Boolean) Tag() Tag { return TagBoolean }

// Tag implements [Value].
func (OctetString) Tag() Tag { return TagOctetString }

// Tag implements [Value].
func (Text) Tag() Tag { return TagText }

// Tag implements [Value].
func (Name) Tag() Tag { return TagName }

// Tag implements [Value].
func (Keyword) Tag() Tag { return TagKeyword }

// Tag implements [Value].
func (URI) Tag() Tag { return TagURI }

// Tag implements [Value].
func (URIScheme) Tag() Tag { return TagURIScheme }

// Tag implements [Value].
func (Charset) Tag() Tag { return TagCharset }

// Tag implements [Value].
func (NaturalLanguage) Tag() Tag { return TagNaturalLanguage }

// Tag implements [Value].
func (MimeMediaType) Tag() Tag { return TagMimeMediaType }

// Tag implements [Value].
func (TextWithLanguage) Tag() Tag { return TagTextWithLanguage }

// Tag implements [Value].
func (NameWithLanguage) Tag() Tag { return TagNameWithLanguage }

// Tag implements [Value].
func (Resolution) Tag() Tag { return TagResolution }

// Tag implements [Value].
func (Range) Tag() Tag { return TagRange }

// Tag implements [Value].
func (DateTime) Tag() Tag { return TagDateTime }

// Tag implements [Value].
func (Collection) Tag() Tag { return TagBeginCollection }

// Tag implements [Value].
func (o OutOfBand) Tag() Tag { return Tag(o) }

// Tag implements [Value].
func (r Raw) Tag() Tag { return r.ValueTag }

// String returns the decimal value.
func (v Integer) String() string { return strconv.Itoa(int(v)) }

// String returns the decimal value.
func (v Enum) String() string { return strconv.Itoa(int(v)) }

// String returns "true" or "false".
func (v Boolean) String() string { return strconv.FormatBool(bool(v)) }

// String returns the octets as a string.
func (v OctetString) String() string { return string(v) }

// String returns the text.
func (v Text) String() string { return string(v) }

// String returns the name.
func (v Name) String() string { return string(v) }

// String returns the keyword.
func (v Keyword) String() string { return string(v) }

// String returns the URI.
func (v URI) String() string { return string(v) }

// String returns the scheme.
func (v URIScheme) String() string { return string(v) }

// String returns the charset.
func (v Charset) String() string { return string(v) }

// String returns the language tag.
func (v NaturalLanguage) String() string { return string(v) }

// String returns the media type.
func (v MimeMediaType) String() string { return string(v) }

// String returns the text without the language.
func (v TextWithLanguage) String() string { return v.Text }

// String returns the name without the language.
func (v NameWithLanguage) String() string { return v.Name }

// String returns e.g. "600x600dpi".
func (v Resolution) String() string {
	return fmt.Sprintf("%dx%d%s", v.X, v.Y, v.Units)
}

// String returns e.g. "1-10".
func (v Range) String() string { return fmt.Sprintf("%d-%d", v.Lower, v.Upper) }

// String returns an ISO 8601 representation such as
// "2024-05-01T13:04:05.3+02:00".
func (v DateTime) String() string {
	dir := v.UTCDirection
	if dir != '-' {
		dir = '+'
	}
	return fmt.Sprintf("%04d-%02d-%02dT%02d:%02d:%02d.%d%c%02d:%02d",
		v.Year, v.Month, v.Day, v.Hour, v.Minute, v.Second, v.Deciseconds,
		dir, v.UTCHours, v.UTCMinutes)
}

// String returns the members in the form "{name=value name=v1,v2}".
func (v Collection) String() string {
	var b strings.Builder
	b.WriteByte('{')
	for i, a := range v {
		if i > 0 {
			b.WriteByte(' ')
		}
		b.WriteString(a.Name)
		b.WriteByte('=')
		for j, x := range a.Values {
			if j > 0 {
				b.WriteByte(',')
			}
			b.WriteString(valueString(x))
		}
	}
	b.WriteByte('}')
	return b.String()
}

// String returns the name of the out-of-band tag, e.g. "no-value".
func (o OutOfBand) String() string { return Tag(o).String() }

// String returns the tag and the data in hex.
func (r Raw) String() string { return fmt.Sprintf("%s:%x", r.ValueTag, r.Data) }

func valueString(v Value) string {
	if v == nil {
		return "<nil>"
	}
	return v.String()
}

func (Integer) isValue()          {}
func (Enum) isValue()             {}
func (Boolean) isValue()          {}
func (OctetString) isValue()      {}
func (Text) isValue()             {}
func (Name) isValue()             {}
func (Keyword) isValue()          {}
func (URI) isValue()              {}
func (URIScheme) isValue()        {}
func (Charset) isValue()          {}
func (NaturalLanguage) isValue()  {}
func (MimeMediaType) isValue()    {}
func (TextWithLanguage) isValue() {}
func (NameWithLanguage) isValue() {}
func (Resolution) isValue()       {}
func (Range) isValue()            {}
func (DateTime) isValue()         {}
func (Collection) isValue()       {}
func (OutOfBand) isValue()        {}
func (Raw) isValue()              {}
