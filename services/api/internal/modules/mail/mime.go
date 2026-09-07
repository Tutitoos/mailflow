package mail

import (
	"bytes"
	"encoding/base64"
	"errors"
	"fmt"
	"io"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"net/url"
	"strings"
	"unicode/utf8"

	xhtml "golang.org/x/net/html"
	"golang.org/x/net/html/atom"
	"golang.org/x/net/html/charset"
)

var (
	ErrMalformedMIME = errors.New("malformed MIME message")
	ErrMIMETooLarge  = errors.New("MIME message exceeds configured limits")
	ErrTooManyParts  = errors.New("MIME message has too many parts")
)

type MIMEPolicy struct {
	MaxRawBytes  int64
	MaxPartBytes int64
	MaxBodyBytes int
	MaxParts     int
}

func DefaultMIMEPolicy() MIMEPolicy {
	return MIMEPolicy{MaxRawBytes: 25 << 20, MaxPartBytes: 10 << 20, MaxBodyBytes: 10 << 20, MaxParts: 256}
}

type NormalizedMessageContent struct {
	Subject     string
	MessageID   string
	References  []string
	InReplyTo   []string
	Addresses   []MessageAddressInput
	BodyText    string
	BodyHTML    string
	Attachments []AttachmentInput
}

type MIMEMessageNormalizer interface {
	Normalize(io.Reader) (NormalizedMessageContent, error)
}

type Normalizer struct {
	policy MIMEPolicy
}

func NewNormalizer(policy MIMEPolicy) (*Normalizer, error) {
	if policy.MaxRawBytes < 1 || policy.MaxPartBytes < 1 || policy.MaxBodyBytes < 1 || policy.MaxParts < 1 {
		return nil, ErrMalformedMIME
	}
	return &Normalizer{policy: policy}, nil
}

func (normalizer *Normalizer) Normalize(source io.Reader) (NormalizedMessageContent, error) {
	raw, err := readBounded(source, normalizer.policy.MaxRawBytes)
	if err != nil {
		return NormalizedMessageContent{}, err
	}
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return NormalizedMessageContent{}, ErrMalformedMIME
	}
	content, err := normalizeHeaders(message.Header)
	if err != nil {
		return NormalizedMessageContent{}, err
	}
	state := mimeState{policy: normalizer.policy, content: content}
	if err := state.consume(textproto.MIMEHeader(message.Header), message.Body); err != nil {
		return NormalizedMessageContent{}, err
	}
	state.content.BodyText = strings.TrimSpace(state.text.String())
	if state.html.Len() > 0 {
		state.content.BodyHTML, err = sanitizeHTML(state.html.String())
		if err != nil {
			return NormalizedMessageContent{}, ErrMalformedMIME
		}
	}
	if state.content.BodyText == "" && state.content.BodyHTML != "" {
		state.content.BodyText = htmlText(state.content.BodyHTML)
	}
	if len(state.content.BodyText) > normalizer.policy.MaxBodyBytes || len(state.content.BodyHTML) > normalizer.policy.MaxBodyBytes {
		return NormalizedMessageContent{}, ErrMIMETooLarge
	}
	return state.content, nil
}

type mimeState struct {
	policy  MIMEPolicy
	parts   int
	text    strings.Builder
	html    strings.Builder
	content NormalizedMessageContent
}

func (state *mimeState) consume(header textproto.MIMEHeader, body io.Reader) error {
	state.parts++
	if state.parts > state.policy.MaxParts {
		return ErrTooManyParts
	}
	mediaType, parameters, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil && header.Get("Content-Type") != "" {
		return ErrMalformedMIME
	}
	if mediaType == "" {
		mediaType = "text/plain"
	}
	mediaType = strings.ToLower(mediaType)
	if strings.HasPrefix(mediaType, "multipart/") {
		boundary := parameters["boundary"]
		if boundary == "" {
			return ErrMalformedMIME
		}
		reader := multipart.NewReader(body, boundary)
		for {
			part, partErr := reader.NextPart()
			if errors.Is(partErr, io.EOF) {
				return nil
			}
			if partErr != nil {
				return ErrMalformedMIME
			}
			if err := state.consume(textproto.MIMEHeader(part.Header), part); err != nil {
				part.Close()
				return err
			}
			part.Close()
		}
	}

	disposition, dispositionParameters, err := parseDisposition(header.Get("Content-Disposition"))
	if err != nil {
		return err
	}
	filename := dispositionParameters["filename"]
	if filename == "" {
		filename = parameters["name"]
	}
	isAttachment := disposition == "attachment" || filename != "" || (mediaType != "text/plain" && mediaType != "text/html")
	decoded, err := decodeTransfer(body, header.Get("Content-Transfer-Encoding"))
	if err != nil {
		return err
	}
	payload, err := readBounded(decoded, state.policy.MaxPartBytes)
	if err != nil {
		return err
	}
	if isAttachment {
		kind := "attachment"
		if disposition == "inline" {
			kind = "inline"
		}
		decodedFilename := decodeHeader(filename)
		contentID := trimMessageID(header.Get("Content-ID"))
		if !utf8.ValidString(decodedFilename) || !utf8.ValidString(contentID) {
			return ErrMalformedMIME
		}
		if len(decodedFilename) > 1024 || len(contentID) > 998 {
			return ErrMIMETooLarge
		}
		state.content.Attachments = append(state.content.Attachments, AttachmentInput{
			Filename: decodedFilename, MediaType: mediaType, Disposition: kind,
			ContentID: contentID, SizeBytes: int64(len(payload)),
		})
		return nil
	}
	decodedText, err := decodeCharset(payload, parameters["charset"])
	if err != nil {
		return ErrMalformedMIME
	}
	if mediaType == "text/html" {
		return appendBounded(&state.html, decodedText, state.policy.MaxBodyBytes)
	}
	return appendBounded(&state.text, decodedText, state.policy.MaxBodyBytes)
}

func normalizeHeaders(header mail.Header) (NormalizedMessageContent, error) {
	content := NormalizedMessageContent{
		Subject: decodeHeader(header.Get("Subject")), MessageID: trimMessageID(header.Get("Message-ID")),
		References: messageIDs(header.Get("References")), InReplyTo: messageIDs(header.Get("In-Reply-To")),
	}
	if !utf8.ValidString(content.Subject) || !utf8.ValidString(content.MessageID) {
		return NormalizedMessageContent{}, ErrMalformedMIME
	}
	if len(content.Subject) > 1<<20 || len(content.MessageID) > 998 || len(content.References) > 100 || len(content.InReplyTo) > 100 {
		return NormalizedMessageContent{}, ErrMIMETooLarge
	}
	for _, identifier := range append(append([]string{}, content.References...), content.InReplyTo...) {
		if !utf8.ValidString(identifier) {
			return NormalizedMessageContent{}, ErrMalformedMIME
		}
		if !boundedText(identifier, 998) {
			return NormalizedMessageContent{}, ErrMIMETooLarge
		}
	}
	roles := []struct {
		header string
		role   AddressRole
	}{{"From", AddressFrom}, {"Sender", AddressSender}, {"Reply-To", AddressReplyTo}, {"To", AddressTo}, {"Cc", AddressCC}, {"Bcc", AddressBCC}}
	for _, item := range roles {
		value := header.Get(item.header)
		if value == "" {
			continue
		}
		addresses, err := mail.ParseAddressList(value)
		if err != nil {
			return NormalizedMessageContent{}, ErrMalformedMIME
		}
		for _, address := range addresses {
			if !utf8.ValidString(address.Address) || !utf8.ValidString(address.Name) {
				return NormalizedMessageContent{}, ErrMalformedMIME
			}
			if len(content.Addresses) >= 512 || !boundedText(address.Address, 1024) || len(address.Name) > 256 {
				return NormalizedMessageContent{}, ErrMIMETooLarge
			}
			content.Addresses = append(content.Addresses, MessageAddressInput{Role: item.role, DisplayName: address.Name, Address: address.Address})
		}
	}
	return content, nil
}

func readBounded(reader io.Reader, limit int64) ([]byte, error) {
	payload, err := io.ReadAll(io.LimitReader(reader, limit+1))
	if err != nil {
		return nil, ErrMalformedMIME
	}
	if int64(len(payload)) > limit {
		return nil, ErrMIMETooLarge
	}
	return payload, nil
}

func decodeTransfer(reader io.Reader, encoding string) (io.Reader, error) {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "", "7bit", "8bit", "binary":
		return reader, nil
	case "base64":
		return base64.NewDecoder(base64.StdEncoding, reader), nil
	case "quoted-printable":
		return quotedprintable.NewReader(reader), nil
	default:
		return nil, ErrMalformedMIME
	}
}

func decodeCharset(payload []byte, label string) (string, error) {
	if label == "" || strings.EqualFold(label, "utf-8") || strings.EqualFold(label, "us-ascii") {
		if !utf8.Valid(payload) {
			return "", ErrMalformedMIME
		}
		return string(payload), nil
	}
	reader, err := charset.NewReaderLabel(label, bytes.NewReader(payload))
	if err != nil {
		return "", err
	}
	decoded, err := io.ReadAll(reader)
	return string(decoded), err
}

func appendBounded(builder *strings.Builder, value string, limit int) error {
	separator := 0
	if builder.Len() > 0 {
		separator = 1
	}
	if builder.Len()+separator+len(value) > limit {
		return ErrMIMETooLarge
	}
	if separator > 0 {
		builder.WriteByte('\n')
	}
	builder.WriteString(value)
	return nil
}

func parseDisposition(value string) (string, map[string]string, error) {
	if value == "" {
		return "", map[string]string{}, nil
	}
	disposition, parameters, err := mime.ParseMediaType(value)
	if err != nil {
		return "", nil, ErrMalformedMIME
	}
	if disposition != "inline" && disposition != "attachment" {
		return "", nil, ErrMalformedMIME
	}
	return strings.ToLower(disposition), parameters, nil
}

func decodeHeader(value string) string {
	decoded, err := new(mime.WordDecoder).DecodeHeader(value)
	if err != nil {
		return strings.TrimSpace(value)
	}
	return strings.TrimSpace(decoded)
}

func messageIDs(value string) []string {
	fields := strings.Fields(value)
	result := make([]string, 0, len(fields))
	for _, field := range fields {
		if id := trimMessageID(field); id != "" {
			result = append(result, id)
		}
	}
	return result
}

func trimMessageID(value string) string {
	return strings.Trim(strings.TrimSpace(value), "<>")
}

var safeElements = map[string]bool{
	"a": true, "abbr": true, "b": true, "blockquote": true, "br": true, "code": true,
	"del": true, "div": true, "em": true, "h1": true, "h2": true, "h3": true,
	"h4": true, "h5": true, "h6": true, "hr": true, "i": true, "img": true, "li": true,
	"ol": true, "p": true, "pre": true, "span": true, "strong": true, "sub": true,
	"sup": true, "table": true, "tbody": true, "td": true, "tfoot": true, "th": true,
	"thead": true, "tr": true, "u": true, "ul": true,
}

var discardedElements = map[string]bool{
	"applet": true, "base": true, "button": true, "embed": true, "form": true, "frame": true,
	"frameset": true, "iframe": true, "input": true, "link": true, "math": true, "meta": true,
	"object": true, "script": true, "select": true, "style": true, "svg": true, "textarea": true,
}

func sanitizeHTML(value string) (string, error) {
	context := &xhtml.Node{Type: xhtml.ElementNode, Data: "div", DataAtom: atom.Div}
	nodes, err := xhtml.ParseFragment(strings.NewReader(value), context)
	if err != nil {
		return "", err
	}
	root := &xhtml.Node{Type: xhtml.ElementNode, Data: "div", DataAtom: atom.Div}
	for _, node := range nodes {
		for _, sanitized := range sanitizeNode(node) {
			root.AppendChild(sanitized)
		}
	}
	var output strings.Builder
	for child := root.FirstChild; child != nil; child = child.NextSibling {
		if err := xhtml.Render(&output, child); err != nil {
			return "", err
		}
	}
	return output.String(), nil
}

func sanitizeNode(node *xhtml.Node) []*xhtml.Node {
	if node.Type == xhtml.TextNode {
		return []*xhtml.Node{{Type: xhtml.TextNode, Data: node.Data}}
	}
	if node.Type != xhtml.ElementNode || discardedElements[node.Data] {
		if node.Type == xhtml.ElementNode && discardedElements[node.Data] {
			return nil
		}
		return sanitizeChildren(node)
	}
	if !safeElements[node.Data] {
		return sanitizeChildren(node)
	}
	clone := &xhtml.Node{Type: xhtml.ElementNode, Data: node.Data, DataAtom: node.DataAtom}
	for _, attribute := range node.Attr {
		if node.Data == "img" && (strings.EqualFold(attribute.Key, "src") || strings.EqualFold(attribute.Key, "data-mailflow-src")) {
			if safeRemoteImageURL(attribute.Val) {
				clone.Attr = append(clone.Attr, xhtml.Attribute{Key: "data-mailflow-src", Val: strings.TrimSpace(attribute.Val)})
			}
			continue
		}
		if safeAttribute(node.Data, attribute.Key, attribute.Val) {
			clone.Attr = append(clone.Attr, xhtml.Attribute{Key: attribute.Key, Val: attribute.Val})
		}
	}
	if node.Data == "img" {
		hasSource := false
		for _, attribute := range clone.Attr {
			if attribute.Key == "data-mailflow-src" {
				hasSource = true
				break
			}
		}
		if !hasSource {
			return nil
		}
	}
	if node.Data == "a" {
		clone.Attr = append(clone.Attr, xhtml.Attribute{Key: "rel", Val: "noopener noreferrer"})
	}
	for _, child := range sanitizeChildren(node) {
		clone.AppendChild(child)
	}
	return []*xhtml.Node{clone}
}

func sanitizeChildren(node *xhtml.Node) []*xhtml.Node {
	var result []*xhtml.Node
	for child := node.FirstChild; child != nil; child = child.NextSibling {
		result = append(result, sanitizeNode(child)...)
	}
	return result
}

func safeAttribute(element, key, value string) bool {
	key = strings.ToLower(key)
	if key == "title" || key == "dir" || key == "lang" || ((element == "td" || element == "th") && (key == "colspan" || key == "rowspan")) {
		return true
	}
	if element == "img" {
		return key == "alt" || ((key == "width" || key == "height") && safeImageDimension(value))
	}
	if element != "a" || key != "href" {
		return false
	}
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	return scheme == "" || scheme == "http" || scheme == "https" || scheme == "mailto"
}

func safeRemoteImageURL(value string) bool {
	parsed, err := url.Parse(strings.TrimSpace(value))
	if err != nil || parsed.Host == "" {
		return false
	}
	scheme := strings.ToLower(parsed.Scheme)
	return scheme == "http" || scheme == "https"
}

func safeImageDimension(value string) bool {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 4 {
		return false
	}
	for _, character := range value {
		if character < '0' || character > '9' {
			return false
		}
	}
	return true
}

func htmlText(value string) string {
	document, err := xhtml.Parse(strings.NewReader(value))
	if err != nil {
		return ""
	}
	var output strings.Builder
	var walk func(*xhtml.Node)
	walk = func(node *xhtml.Node) {
		if node.Type == xhtml.TextNode {
			output.WriteString(node.Data)
		}
		if node.Type == xhtml.ElementNode && (node.Data == "br" || node.Data == "p" || node.Data == "div" || node.Data == "li") {
			output.WriteByte('\n')
		}
		for child := node.FirstChild; child != nil; child = child.NextSibling {
			walk(child)
		}
	}
	walk(document)
	return strings.TrimSpace(output.String())
}

func (content NormalizedMessageContent) Apply(input *UpsertMessageInput) error {
	if input == nil {
		return fmt.Errorf("apply normalized MIME: %w", ErrMalformedMIME)
	}
	input.MessageID = content.MessageID
	input.References = append([]string{}, content.References...)
	input.InReplyTo = append([]string{}, content.InReplyTo...)
	input.Subject = content.Subject
	input.BodyText = content.BodyText
	input.BodyHTML = content.BodyHTML
	input.Addresses = append([]MessageAddressInput{}, content.Addresses...)
	input.Attachments = append([]AttachmentInput{}, content.Attachments...)
	return nil
}
