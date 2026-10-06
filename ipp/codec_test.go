package ipp

import (
	"bytes"
	"encoding/binary"
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
	"time"
)

// Wire-format builders for hand-crafted messages.

func header(version uint16, code uint16, id uint32) []byte {
	b := binary.BigEndian.AppendUint16(nil, version)
	b = binary.BigEndian.AppendUint16(b, code)
	return binary.BigEndian.AppendUint32(b, id)
}

// wattr encodes one attribute value: tag, name-length, name, value-length,
// value.
func wattr(tag Tag, name string, value []byte) []byte {
	b := []byte{byte(tag)}
	b = binary.BigEndian.AppendUint16(b, uint16(len(name)))
	b = append(b, name...)
	b = binary.BigEndian.AppendUint16(b, uint16(len(value)))
	return append(b, value...)
}

func cat(parts ...[]byte) []byte { return bytes.Join(parts, nil) }

func i32(n int32) []byte { return binary.BigEndian.AppendUint32(nil, uint32(n)) }

func attr(name string, vs ...Value) Attribute { return Attribute{Name: name, Values: vs} }

// mediaCol returns a media-col collection as sent by CUPS.
func mediaCol(x, y int32, source string) Collection {
	return Collection{
		attr("media-size", Collection{
			attr("x-dimension", Integer(x)),
			attr("y-dimension", Integer(y)),
		}),
		attr("media-bottom-margin", Integer(423)),
		attr("media-left-margin", Integer(423)),
		attr("media-right-margin", Integer(423)),
		attr("media-top-margin", Integer(423)),
		attr("media-source", Keyword(source)),
		attr("media-type", Keyword("stationery")),
	}
}

// sampleMessages returns realistic CUPS requests and responses. They are
// used for round-trip tests and as fuzz seeds.
func sampleMessages() map[string]*Message {
	opGroup := func(extra ...Attribute) Group {
		return Group{Tag: TagOperationGroup, Attrs: append(Attributes{
			attr("attributes-charset", Charset("utf-8")),
			attr("attributes-natural-language", NaturalLanguage("en-us")),
		}, extra...)}
	}
	printerURI := attr("printer-uri", URI("ipp://localhost/printers/Office"))
	user := attr("requesting-user-name", Name("tim"))
	stamp := DateTime{2024, 5, 1, 13, 4, 5, 3, '+', 2, 0}
	return map[string]*Message{
		"cups-get-printers": {Version: Version20, Code: uint16(OpCUPSGetPrinters), RequestID: 1,
			Groups: []Group{opGroup(user, attr("requested-attributes",
				Keyword("printer-name"), Keyword("printer-uri-supported"), Keyword("printer-state")))}},
		"get-printer-attributes": {Version: Version11, Code: uint16(OpGetPrinterAttributes), RequestID: 2,
			Groups: []Group{opGroup(printerURI, user, attr("requested-attributes", Keyword("all")))}},
		"printer-attributes-response": {Version: Version20, Code: uint16(StatusOK), RequestID: 2,
			Groups: []Group{
				opGroup(),
				{Tag: TagPrinterGroup, Attrs: Attributes{
					attr("printer-uri-supported", URI("ipp://localhost/printers/Office"), URI("ipps://localhost/printers/Office")),
					attr("uri-security-supported", Keyword("none"), Keyword("tls")),
					attr("printer-name", NameWithLanguage{"en", "Office"}),
					attr("printer-info", TextWithLanguage{"de-de", "Büro-Drucker"}),
					attr("printer-location", Text("2nd floor")),
					attr("printer-state", Enum(PrinterIdle)),
					attr("printer-state-reasons", Keyword("none")),
					attr("printer-is-accepting-jobs", Boolean(true)),
					attr("printer-up-time", Integer(123456)),
					attr("printer-current-time", stamp),
					attr("printer-resolution-supported", Resolution{300, 300, UnitsDPI}, Resolution{600, 1200, UnitsDPI}, Resolution{118, 118, UnitsDPCM}),
					attr("copies-supported", Range{1, 999}),
					attr("document-format-supported", MimeMediaType("application/pdf"), MimeMediaType("image/urf")),
					attr("uri-authentication-supported", Keyword("requesting-user-name")),
					attr("reference-uri-schemes-supported", URIScheme("http"), URIScheme("https")),
					attr("generated-natural-language-supported", NaturalLanguage("en")),
					attr("charset-supported", Charset("utf-8")),
					attr("printer-alert", OctetString("code=other")),
					attr("media-default", Keyword("iso_a4_210x297mm")),
					attr("media-col-default", mediaCol(21000, 29700, "auto")),
					attr("media-col-database", mediaCol(21000, 29700, "tray-1"), mediaCol(21590, 27940, "manual")),
					attr("printer-geo-location", Unknown),
					attr("printer-organization", NoValue),
					attr("printer-state-change-date-time", stamp),
					attr("marker-names", Name("Black"), NameWithLanguage{"en", "Cyan"}),
					attr("vendor-extension", Raw{ValueTag: 0x4F, Data: []byte("x")}, Raw{ValueTag: TagExtension, Data: []byte{0, 0, 0x12, 0x34, 1}}),
				}},
			}},
		"print-job": {Version: Version11, Code: uint16(OpPrintJob), RequestID: 3,
			Groups: []Group{
				opGroup(printerURI, user, attr("job-name", Name("Invoice 4711")), attr("document-format", MimeMediaType("application/pdf"))),
				{Tag: TagJobGroup, Attrs: Attributes{
					attr("copies", Integer(2)),
					attr("sides", Keyword("two-sided-long-edge")),
					attr("print-color-mode", Keyword("monochrome")),
					attr("page-ranges", Range{1, 3}, Range{5, 5}),
					attr("media-col", mediaCol(21000, 29700, "tray-1")),
					attr("printer-resolution", Resolution{600, 600, UnitsDPI}),
				}},
			}},
		"print-job-response": {Version: Version11, Code: uint16(StatusOKIgnoredOrSubstituted), RequestID: 3,
			Groups: []Group{
				opGroup(attr("status-message", Text("successful-ok-ignored-or-substituted-attributes"))),
				{Tag: TagUnsupportedGroup, Attrs: Attributes{attr("print-color-mode", Keyword("monochrome")), attr("finishings", Unsupported)}},
				{Tag: TagJobGroup, Attrs: Attributes{
					attr("job-id", Integer(42)),
					attr("job-uri", URI("ipp://localhost/jobs/42")),
					attr("job-state", Enum(JobPending)),
					attr("job-state-reasons", Keyword("none")),
				}},
			}},
		"get-jobs-response": {Version: Version20, Code: uint16(StatusOK), RequestID: 4,
			Groups: []Group{
				opGroup(),
				{Tag: TagJobGroup, Attrs: Attributes{attr("job-id", Integer(1)), attr("job-state", Enum(JobCompleted))}},
				{Tag: TagJobGroup, Attrs: Attributes{attr("job-id", Integer(2)), attr("job-state", Enum(JobProcessing))}},
				{Tag: TagJobGroup},
			}},
		"error-response": {Version: Version20, Code: uint16(StatusErrorNotFound), RequestID: 5,
			Groups: []Group{opGroup(attr("status-message", Text("The printer or class does not exist.")))}},
		"cancel-job": {Version: Version20, Code: uint16(OpCancelJob), RequestID: 0x7fffffff,
			Groups: []Group{opGroup(printerURI, attr("job-id", Integer(42)), user, attr("purge-job", Boolean(false)))}},
		"empty": {Version: Version20, Code: uint16(OpCUPSGetDefault), RequestID: -1},
		"extremes": {Version: 0xffff, Code: 0xffff, RequestID: -2147483648,
			Groups: []Group{{Tag: 0x0f, Attrs: Attributes{
				attr("i", Integer(-2147483648), Integer(2147483647), Enum(-1)),
				attr("s", Text(""), OctetString(nil), Keyword(strings.Repeat("k", maxLength))),
				attr("oob", OutOfBand(0x11), OutOfBand(0x1f), OutOfBand(TagNotSettable), OutOfBand(TagDeleteAttribute), OutOfBand(TagAdminDefine)),
				attr("c", Collection(nil), Collection{attr("", Collection{attr("deep", nestedCollection(MaxCollectionDepth-2))})}),
				attr("r", Raw{ValueTag: 0xff}),
			}}}},
	}
}

func nestedCollection(depth int) Collection {
	c := Collection{attr("leaf", Boolean(true))}
	for range depth - 1 {
		c = Collection{attr("n", c)}
	}
	return c
}

func TestRoundTrip(t *testing.T) {
	for name, m := range sampleMessages() {
		t.Run(name, func(t *testing.T) {
			b, err := m.MarshalBinary()
			if err != nil {
				t.Fatalf("MarshalBinary: %v", err)
			}
			var got Message
			if err := got.UnmarshalBinary(b); err != nil {
				t.Fatalf("UnmarshalBinary: %v", err)
			}
			if !reflect.DeepEqual(&got, m) {
				t.Errorf("round trip mismatch:\n got %+v\nwant %+v", &got, m)
			}
			// Streaming decode with trailing document data.
			doc := []byte("%PDF-1.7 document data")
			r := bytes.NewReader(append(b, doc...))
			got2, err := Decode(r)
			if err != nil {
				t.Fatalf("Decode: %v", err)
			}
			if !reflect.DeepEqual(got2, m) {
				t.Errorf("Decode mismatch:\n got %+v\nwant %+v", got2, m)
			}
			rest, _ := io.ReadAll(r)
			if !bytes.Equal(rest, doc) {
				t.Errorf("document data = %q, want %q", rest, doc)
			}
			var buf bytes.Buffer
			if err := m.Encode(&buf); err != nil || !bytes.Equal(buf.Bytes(), b) {
				t.Errorf("Encode = %x, %v; want %x", buf.Bytes(), err, b)
			}
		})
	}
}

// TestEncodeWire checks the exact encoding against a hand-built message
// following RFC 8010 §3.
func TestEncodeWire(t *testing.T) {
	m := &Message{Version: Version20, Code: uint16(OpGetPrinterAttributes), RequestID: 7, Groups: []Group{
		{Tag: TagOperationGroup, Attrs: Attributes{
			attr("attributes-charset", Charset("utf-8")),
			attr("requested-attributes", Keyword("a"), Keyword("bc")),
			attr("printer-name", NameWithLanguage{"en", "P"}),
			attr("ok", Boolean(true)),
			attr("res", Resolution{600, 300, UnitsDPI}),
			attr("t", DateTime{2024, 12, 31, 23, 59, 60, 9, '-', 5, 30}),
			attr("media-col", Collection{attr("media-size", Collection{attr("x-dimension", Integer(21000))})}),
			attr("none", NoValue),
		}},
	}}
	want := cat(
		header(0x0200, 0x000B, 7),
		[]byte{0x01},
		wattr(TagCharset, "attributes-charset", []byte("utf-8")),
		wattr(TagKeyword, "requested-attributes", []byte("a")),
		wattr(TagKeyword, "", []byte("bc")),
		wattr(TagNameWithLanguage, "printer-name", []byte{0, 2, 'e', 'n', 0, 1, 'P'}),
		wattr(TagBoolean, "ok", []byte{1}),
		wattr(TagResolution, "res", cat(i32(600), i32(300), []byte{3})),
		wattr(TagDateTime, "t", []byte{0x07, 0xE8, 12, 31, 23, 59, 60, 9, '-', 5, 30}),
		wattr(TagBeginCollection, "media-col", nil),
		wattr(TagMemberName, "", []byte("media-size")),
		wattr(TagBeginCollection, "", nil),
		wattr(TagMemberName, "", []byte("x-dimension")),
		wattr(TagInteger, "", i32(21000)),
		wattr(TagEndCollection, "", nil),
		wattr(TagEndCollection, "", nil),
		wattr(TagNoValue, "none", nil),
		[]byte{0x03},
	)
	got, err := m.MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, want) {
		t.Fatalf("MarshalBinary:\n got %x\nwant %x", got, want)
	}
}

// TestDecodeLenient checks inputs that are accepted although non-canonical.
// lenientCases are non-canonical inputs the decoder accepts.
var lenientCases = []struct {
	name string
	in   []byte
	want []Group
}{
	{"boolean other than 0/1",
		cat(header(0x200, 0, 1), []byte{1}, wattr(TagBoolean, "b", []byte{7}), []byte{3}),
		[]Group{{TagOperationGroup, Attributes{attr("b", Boolean(true))}}}},
	{"out-of-band with data",
		cat(header(0x200, 0, 1), []byte{1}, wattr(TagUnknown, "u", []byte("junk")), []byte{3}),
		[]Group{{TagOperationGroup, Attributes{attr("u", Unknown)}}}},
	{"begCollection and endCollection with data",
		cat(header(0x200, 0, 1), []byte{4}, wattr(TagBeginCollection, "c", []byte("x")),
			wattr(TagMemberName, "", []byte("m")), wattr(TagInteger, "", i32(1)), wattr(TagInteger, "", i32(2)),
			wattr(TagEndCollection, "", []byte("y")), []byte{3}),
		[]Group{{TagPrinterGroup, Attributes{attr("c", Collection{attr("m", Integer(1), Integer(2))})}}}},
	{"empty group",
		cat(header(0x200, 0, 1), []byte{1, 2}, []byte{3}),
		[]Group{{Tag: TagOperationGroup}, {Tag: TagJobGroup}}},
}

func TestDecodeLenient(t *testing.T) {
	for _, tt := range lenientCases {
		t.Run(tt.name, func(t *testing.T) {
			var m Message
			if err := m.UnmarshalBinary(tt.in); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(m.Groups, tt.want) {
				t.Errorf("got %+v, want %+v", m.Groups, tt.want)
			}
		})
	}
}

func TestDecodeErrors(t *testing.T) {
	h := header(0x200, 0x0B, 1)
	op := []byte{1}
	charset := wattr(TagCharset, "attributes-charset", []byte("utf-8"))
	deep := op
	for range MaxCollectionDepth + 1 {
		deep = cat(deep, wattr(TagBeginCollection, "c", nil), wattr(TagMemberName, "", []byte("m")))
	}
	tests := []struct {
		name string
		in   []byte
	}{
		{"empty", nil},
		{"short header", h[:5]},
		{"no end tag", h},
		{"no end tag after group", cat(h, op, charset)},
		{"reserved delimiter", cat(h, []byte{0, 3})},
		{"attribute before group", cat(h, charset, []byte{3})},
		{"additional value first", cat(h, op, wattr(TagKeyword, "", []byte("x")), []byte{3})},
		{"truncated name length", cat(h, op, []byte{0x44, 0})},
		{"name exceeds input", cat(h, op, []byte{0x44, 0xff, 0xff, 'a'})},
		{"value exceeds input", cat(h, op, []byte{0x44, 0, 1, 'a', 0xff, 0xff, 'b'})},
		{"integer length", cat(h, op, wattr(TagInteger, "i", []byte{1, 2, 3}), []byte{3})},
		{"enum length", cat(h, op, wattr(TagEnum, "e", nil), []byte{3})},
		{"boolean length", cat(h, op, wattr(TagBoolean, "b", []byte{0, 1}), []byte{3})},
		{"dateTime length", cat(h, op, wattr(TagDateTime, "d", make([]byte, 10)), []byte{3})},
		{"resolution length", cat(h, op, wattr(TagResolution, "r", make([]byte, 8)), []byte{3})},
		{"range length", cat(h, op, wattr(TagRange, "r", make([]byte, 9)), []byte{3})},
		{"textWithLanguage short", cat(h, op, wattr(TagTextWithLanguage, "t", []byte{0}), []byte{3})},
		{"textWithLanguage lang overflow", cat(h, op, wattr(TagTextWithLanguage, "t", []byte{0, 9, 'e', 'n', 0, 0}), []byte{3})},
		{"textWithLanguage text mismatch", cat(h, op, wattr(TagNameWithLanguage, "t", []byte{0, 2, 'e', 'n', 0, 5, 'x'}), []byte{3})},
		{"textWithLanguage trailing", cat(h, op, wattr(TagTextWithLanguage, "t", []byte{0, 0, 0, 1, 'x', 'y'}), []byte{3})},
		{"memberAttrName at top level", cat(h, op, wattr(TagMemberName, "m", []byte("x")), []byte{3})},
		{"endCollection at top level", cat(h, op, wattr(TagEndCollection, "e", nil), []byte{3})},
		{"unterminated collection", cat(h, op, wattr(TagBeginCollection, "c", nil), []byte{3})},
		{"collection value without member", cat(h, op, wattr(TagBeginCollection, "c", nil), wattr(TagInteger, "", i32(1)), wattr(TagEndCollection, "", nil), []byte{3})},
		{"named value in collection", cat(h, op, wattr(TagBeginCollection, "c", nil), wattr(TagMemberName, "", []byte("m")), wattr(TagInteger, "x", i32(1)), wattr(TagEndCollection, "", nil), []byte{3})},
		{"member without value at end", cat(h, op, wattr(TagBeginCollection, "c", nil), wattr(TagMemberName, "", []byte("m")), wattr(TagEndCollection, "", nil), []byte{3})},
		{"member without value before next", cat(h, op, wattr(TagBeginCollection, "c", nil), wattr(TagMemberName, "", []byte("m")), wattr(TagMemberName, "", []byte("n")), wattr(TagInteger, "", i32(1)), wattr(TagEndCollection, "", nil), []byte{3})},
		{"delimiter in collection", cat(h, op, wattr(TagBeginCollection, "c", nil), []byte{2, 0, 0}, []byte{3})},
		{"truncated endCollection", cat(h, op, wattr(TagBeginCollection, "c", nil), []byte{0x37, 0, 0})},
		{"truncated member name", cat(h, op, wattr(TagBeginCollection, "c", nil), []byte{0x4A, 0, 0, 0, 5, 'a'})},
		{"error in member value", cat(h, op, wattr(TagBeginCollection, "c", nil), wattr(TagMemberName, "", []byte("m")), wattr(TagInteger, "", nil), wattr(TagEndCollection, "", nil), []byte{3})},
		{"too deep", cat(deep, wattr(TagInteger, "", i32(1)))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var m Message
			err := m.UnmarshalBinary(tt.in)
			if !errors.Is(err, ErrMalformed) {
				t.Errorf("UnmarshalBinary err = %v, want ErrMalformed", err)
			}
			_, err = Decode(bytes.NewReader(tt.in))
			if !errors.Is(err, ErrMalformed) {
				t.Errorf("Decode err = %v, want ErrMalformed", err)
			}
		})
	}
}

func TestDecodeTruncatedIsUnexpectedEOF(t *testing.T) {
	b, err := sampleMessages()["print-job"].MarshalBinary()
	if err != nil {
		t.Fatal(err)
	}
	for n := range len(b) {
		var m Message
		if err := m.UnmarshalBinary(b[:n]); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("UnmarshalBinary(%d of %d bytes) err = %v, want io.ErrUnexpectedEOF", n, len(b), err)
		}
		if _, err := Decode(bytes.NewReader(b[:n])); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatalf("Decode(%d of %d bytes) err = %v, want io.ErrUnexpectedEOF", n, len(b), err)
		}
	}
}

func TestUnmarshalTrailingData(t *testing.T) {
	b := cat(header(0x200, 0, 1), []byte{3, 'x'})
	var m Message
	if err := m.UnmarshalBinary(b); !errors.Is(err, ErrMalformed) {
		t.Errorf("err = %v, want ErrMalformed", err)
	}
}

type errReader struct{ err error }

func (r errReader) Read([]byte) (int, error) { return 0, r.err }

func TestDecodeReadError(t *testing.T) {
	boom := errors.New("boom")
	if _, err := Decode(errReader{boom}); !errors.Is(err, boom) || !errors.Is(err, ErrMalformed) {
		t.Errorf("err = %v, want boom and ErrMalformed", err)
	}
}

// infinite yields an endless stream of empty operation groups.
type infinite struct{ n int }

func (r *infinite) Read(p []byte) (int, error) {
	for i := range p {
		if r.n < 8 {
			p[i] = 0x02 // header bytes
		} else {
			p[i] = byte(TagOperationGroup)
		}
		r.n++
	}
	return len(p), nil
}

func TestDecodeSizeLimit(t *testing.T) {
	defer func(n int64) { streamLimit = n }(streamLimit)
	streamLimit = 1 << 16
	if _, err := Decode(&infinite{}); !errors.Is(err, ErrMalformed) || !strings.Contains(err.Error(), "exceeds") {
		t.Errorf("err = %v, want size limit error", err)
	}
}

func TestEncodeErrors(t *testing.T) {
	long := strings.Repeat("x", maxLength+1)
	msg := func(g Group) *Message { return &Message{Version: Version20, Groups: []Group{g}} }
	op := func(as ...Attribute) *Message { return msg(Group{Tag: TagOperationGroup, Attrs: as}) }
	tests := []struct {
		name string
		m    *Message
	}{
		{"zero group tag", msg(Group{})},
		{"end group tag", msg(Group{Tag: TagEnd})},
		{"value group tag", msg(Group{Tag: TagInteger})},
		{"empty name", op(attr("", Integer(1)))},
		{"no values", op(attr("a"))},
		{"nil value", op(attr("a", nil))},
		{"long name", op(attr(long, Integer(1)))},
		{"long value", op(attr("a", Text(long)))},
		{"long additional value", op(attr("a", Text("x"), Keyword(long)))},
		{"long with language", op(attr("a", TextWithLanguage{"en", long[4:]}))},
		{"long name with language", op(attr("a", NameWithLanguage{long, ""}))},
		{"bad out-of-band", op(attr("a", OutOfBand(TagInteger)))},
		{"raw with known tag", op(attr("a", Raw{ValueTag: TagKeyword}))},
		{"raw with out-of-band tag", op(attr("a", Raw{ValueTag: TagNoValue}))},
		{"raw with delimiter tag", op(attr("a", Raw{ValueTag: TagJobGroup}))},
		{"empty member", op(attr("a", Collection{attr("m")}))},
		{"long member name", op(attr("a", Collection{attr(long, Integer(1))}))},
		{"bad member value", op(attr("a", Collection{attr("m", Raw{ValueTag: TagText})}))},
		{"too deep", op(attr("a", nestedCollection(MaxCollectionDepth+1)))},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := tt.m.MarshalBinary(); !errors.Is(err, ErrInvalid) {
				t.Errorf("MarshalBinary err = %v, want ErrInvalid", err)
			}
			if err := tt.m.Encode(io.Discard); !errors.Is(err, ErrInvalid) {
				t.Errorf("Encode err = %v, want ErrInvalid", err)
			}
		})
	}
	// The deepest allowed collection encodes and decodes.
	m := op(attr("a", nestedCollection(MaxCollectionDepth)))
	b, err := m.MarshalBinary()
	if err != nil {
		t.Fatalf("depth %d: %v", MaxCollectionDepth, err)
	}
	var got Message
	if err := got.UnmarshalBinary(b); err != nil || !reflect.DeepEqual(&got, m) {
		t.Fatalf("depth %d round trip: %v", MaxCollectionDepth, err)
	}
}

type failWriter struct{}

func (failWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestEncodeWriteError(t *testing.T) {
	if err := NewRequest(OpPrintJob, 1).Encode(failWriter{}); !errors.Is(err, io.ErrClosedPipe) {
		t.Errorf("err = %v, want io.ErrClosedPipe", err)
	}
}

func TestDateTime(t *testing.T) {
	loc := time.FixedZone("", -(5*3600 + 30*60))
	tm := time.Date(2024, 2, 29, 23, 59, 58, 700_000_000, loc)
	d := NewDateTime(tm)
	want := DateTime{2024, 2, 29, 23, 59, 58, 7, '-', 5, 30}
	if d != want {
		t.Fatalf("NewDateTime = %+v, want %+v", d, want)
	}
	if !d.Time().Equal(tm) {
		t.Errorf("Time() = %v, want %v", d.Time(), tm)
	}
	if s := d.String(); s != "2024-02-29T23:59:58.7-05:30" {
		t.Errorf("String() = %q", s)
	}
	utc := NewDateTime(time.Date(2000, 1, 2, 3, 4, 5, 0, time.UTC))
	if s := utc.String(); s != "2000-01-02T03:04:05.0+00:00" {
		t.Errorf("String() = %q", s)
	}
	if s := (DateTime{}).String(); s != "0000-00-00T00:00:00.0+00:00" {
		t.Errorf("zero String() = %q", s)
	}
}

func TestValueStrings(t *testing.T) {
	tests := []struct {
		v    Value
		tag  Tag
		want string
	}{
		{Integer(-5), TagInteger, "-5"},
		{Enum(3), TagEnum, "3"},
		{Boolean(true), TagBoolean, "true"},
		{OctetString("ab"), TagOctetString, "ab"},
		{Text("t"), TagText, "t"},
		{Name("n"), TagName, "n"},
		{Keyword("k"), TagKeyword, "k"},
		{URI("ipp://x"), TagURI, "ipp://x"},
		{URIScheme("ipp"), TagURIScheme, "ipp"},
		{Charset("utf-8"), TagCharset, "utf-8"},
		{NaturalLanguage("en"), TagNaturalLanguage, "en"},
		{MimeMediaType("application/pdf"), TagMimeMediaType, "application/pdf"},
		{TextWithLanguage{"en", "hi"}, TagTextWithLanguage, "hi"},
		{NameWithLanguage{"en", "nm"}, TagNameWithLanguage, "nm"},
		{Resolution{600, 300, UnitsDPI}, TagResolution, "600x300dpi"},
		{Resolution{1, 2, UnitsDPCM}, TagResolution, "1x2dpcm"},
		{Resolution{1, 2, 9}, TagResolution, "1x2units(9)"},
		{Range{1, 9}, TagRange, "1-9"},
		{Collection{attr("a", Integer(1), Integer(2)), attr("b", nil)}, TagBeginCollection, "{a=1,2 b=<nil>}"},
		{NoValue, TagNoValue, "no-value"},
		{Unsupported, TagUnsupportedValue, "unsupported"},
		{Raw{0x4f, []byte{1, 2}}, 0x4f, "tag(0x4f):0102"},
		{DateTime{}, TagDateTime, "0000-00-00T00:00:00.0+00:00"},
	}
	for _, tt := range tests {
		if got := tt.v.String(); got != tt.want {
			t.Errorf("%T.String() = %q, want %q", tt.v, got, tt.want)
		}
		if got := tt.v.Tag(); got != tt.tag {
			t.Errorf("%T.Tag() = %v, want %v", tt.v, got, tt.tag)
		}
		tt.v.isValue()
	}
}

func TestTag(t *testing.T) {
	tests := []struct {
		tag        Tag
		name       string
		delim, oob bool
	}{
		{TagOperationGroup, "operation-attributes-tag", true, false},
		{TagEnd, "end-of-attributes-tag", true, false},
		{0x0f, "tag(0x0f)", true, false},
		{TagUnsupportedValue, "unsupported", false, true},
		{0x1f, "tag(0x1f)", false, true},
		{TagKeyword, "keyword", false, false},
		{TagExtension, "extension", false, false},
	}
	for _, tt := range tests {
		if got := tt.tag.String(); got != tt.name {
			t.Errorf("Tag(0x%02x).String() = %q, want %q", uint8(tt.tag), got, tt.name)
		}
		if tt.tag.IsDelimiter() != tt.delim || tt.tag.IsOutOfBand() != tt.oob {
			t.Errorf("Tag(0x%02x): IsDelimiter/IsOutOfBand wrong", uint8(tt.tag))
		}
	}
}

func FuzzDecode(f *testing.F) {
	for _, m := range sampleMessages() {
		b, err := m.MarshalBinary()
		if err != nil {
			f.Fatal(err)
		}
		f.Add(b)
		f.Add(append(b, "%PDF-1.7"...))
	}
	f.Add([]byte{})
	for _, tt := range lenientCases {
		f.Add(tt.in)
	}
	f.Add(cat(header(0x200, 0, 1), []byte{1}, wattr(TagBeginCollection, "c", nil), wattr(TagMemberName, "", []byte("m")), wattr(TagTextWithLanguage, "", []byte{0, 0, 0, 0}), wattr(TagEndCollection, "", nil), []byte{3}))
	f.Fuzz(func(t *testing.T, data []byte) {
		m, err := Decode(bytes.NewReader(data))
		var m2 Message
		err2 := m2.UnmarshalBinary(data)
		if err != nil {
			if !errors.Is(err, ErrMalformed) {
				t.Fatalf("Decode error does not wrap ErrMalformed: %v", err)
			}
			if err2 == nil {
				t.Fatalf("UnmarshalBinary succeeded where Decode failed: %v", err)
			}
			return
		}
		if err2 == nil && !reflect.DeepEqual(&m2, m) {
			t.Fatalf("UnmarshalBinary and Decode disagree:\n%+v\n%+v", &m2, m)
		}
		b, err := m.MarshalBinary()
		if err != nil {
			t.Fatalf("re-encode of decoded message failed: %v\n%+v", err, m)
		}
		var m3 Message
		if err := m3.UnmarshalBinary(b); err != nil {
			t.Fatalf("decode of re-encoded message failed: %v", err)
		}
		if !reflect.DeepEqual(&m3, m) {
			t.Fatalf("round trip mismatch:\n got %+v\nwant %+v", &m3, m)
		}
		b2, err := m3.MarshalBinary()
		if err != nil || !bytes.Equal(b, b2) {
			t.Fatalf("encoding not stable: %v", err)
		}
	})
}
