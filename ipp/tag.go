package ipp

import "fmt"

// Tag is an IPP tag (RFC 8010 §3.5). Tags 0x00-0x0F are delimiter tags that
// begin an attribute group or end the attribute section; tags 0x10-0xFF are
// value tags that give the syntax of an attribute value.
type Tag uint8

// Delimiter tags.
const (
	TagOperationGroup         Tag = 0x01 // operation-attributes-tag
	TagJobGroup               Tag = 0x02 // job-attributes-tag
	TagEnd                    Tag = 0x03 // end-of-attributes-tag
	TagPrinterGroup           Tag = 0x04 // printer-attributes-tag
	TagUnsupportedGroup       Tag = 0x05 // unsupported-attributes-tag
	TagSubscriptionGroup      Tag = 0x06 // subscription-attributes-tag
	TagEventNotificationGroup Tag = 0x07 // event-notification-attributes-tag
	TagResourceGroup          Tag = 0x08 // resource-attributes-tag
	TagDocumentGroup          Tag = 0x09 // document-attributes-tag
	TagSystemGroup            Tag = 0x0A // system-attributes-tag
)

// Out-of-band value tags.
const (
	TagUnsupportedValue Tag = 0x10 // unsupported
	TagUnknown          Tag = 0x12 // unknown
	TagNoValue          Tag = 0x13 // no-value
	TagNotSettable      Tag = 0x15 // not-settable (RFC 3380)
	TagDeleteAttribute  Tag = 0x16 // delete-attribute (RFC 3380)
	TagAdminDefine      Tag = 0x17 // admin-define (RFC 3380)
)

// Value tags.
const (
	TagInteger          Tag = 0x21 // integer
	TagBoolean          Tag = 0x22 // boolean
	TagEnum             Tag = 0x23 // enum
	TagOctetString      Tag = 0x30 // octetString
	TagDateTime         Tag = 0x31 // dateTime
	TagResolution       Tag = 0x32 // resolution
	TagRange            Tag = 0x33 // rangeOfInteger
	TagBeginCollection  Tag = 0x34 // begCollection
	TagTextWithLanguage Tag = 0x35 // textWithLanguage
	TagNameWithLanguage Tag = 0x36 // nameWithLanguage
	TagEndCollection    Tag = 0x37 // endCollection
	TagText             Tag = 0x41 // textWithoutLanguage
	TagName             Tag = 0x42 // nameWithoutLanguage
	TagKeyword          Tag = 0x44 // keyword
	TagURI              Tag = 0x45 // uri
	TagURIScheme        Tag = 0x46 // uriScheme
	TagCharset          Tag = 0x47 // charset
	TagNaturalLanguage  Tag = 0x48 // naturalLanguage
	TagMimeMediaType    Tag = 0x49 // mimeMediaType
	TagMemberName       Tag = 0x4A // memberAttrName
	TagExtension        Tag = 0x7F // extension: the actual tag follows in the value
)

var tagNames = map[Tag]string{
	TagOperationGroup:         "operation-attributes-tag",
	TagJobGroup:               "job-attributes-tag",
	TagEnd:                    "end-of-attributes-tag",
	TagPrinterGroup:           "printer-attributes-tag",
	TagUnsupportedGroup:       "unsupported-attributes-tag",
	TagSubscriptionGroup:      "subscription-attributes-tag",
	TagEventNotificationGroup: "event-notification-attributes-tag",
	TagResourceGroup:          "resource-attributes-tag",
	TagDocumentGroup:          "document-attributes-tag",
	TagSystemGroup:            "system-attributes-tag",
	TagUnsupportedValue:       "unsupported",
	TagUnknown:                "unknown",
	TagNoValue:                "no-value",
	TagNotSettable:            "not-settable",
	TagDeleteAttribute:        "delete-attribute",
	TagAdminDefine:            "admin-define",
	TagInteger:                "integer",
	TagBoolean:                "boolean",
	TagEnum:                   "enum",
	TagOctetString:            "octetString",
	TagDateTime:               "dateTime",
	TagResolution:             "resolution",
	TagRange:                  "rangeOfInteger",
	TagBeginCollection:        "collection",
	TagTextWithLanguage:       "textWithLanguage",
	TagNameWithLanguage:       "nameWithLanguage",
	TagEndCollection:          "endCollection",
	TagText:                   "textWithoutLanguage",
	TagName:                   "nameWithoutLanguage",
	TagKeyword:                "keyword",
	TagURI:                    "uri",
	TagURIScheme:              "uriScheme",
	TagCharset:                "charset",
	TagNaturalLanguage:        "naturalLanguage",
	TagMimeMediaType:          "mimeMediaType",
	TagMemberName:             "memberAttrName",
	TagExtension:              "extension",
}

// String returns the RFC 8010 name of t, or its hex value if t is unknown.
func (t Tag) String() string {
	if s, ok := tagNames[t]; ok {
		return s
	}
	return fmt.Sprintf("tag(0x%02x)", uint8(t))
}

// IsDelimiter reports whether t is a delimiter tag (0x00-0x0F).
func (t Tag) IsDelimiter() bool { return t < 0x10 }

// IsOutOfBand reports whether t is an out-of-band value tag (0x10-0x1F),
// such as unsupported, unknown or no-value.
func (t Tag) IsOutOfBand() bool { return t >= 0x10 && t < 0x20 }

// known reports whether the codec maps t to a dedicated value type. All
// other value tags outside the out-of-band range decode to [Raw].
func (t Tag) known() bool {
	switch t {
	case TagInteger, TagBoolean, TagEnum,
		TagOctetString, TagDateTime, TagResolution, TagRange,
		TagBeginCollection, TagTextWithLanguage, TagNameWithLanguage, TagEndCollection,
		TagText, TagName, TagKeyword, TagURI, TagURIScheme, TagCharset,
		TagNaturalLanguage, TagMimeMediaType, TagMemberName:
		return true
	}
	return false
}
