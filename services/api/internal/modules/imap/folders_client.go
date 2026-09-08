package imap

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"net"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/Tutitoos/mailflow/services/api/internal/modules/mail"
)

func (prober *NetworkProber) Discover(ctx context.Context, credentials storedCredentials) ([]DiscoveredFolder, error) {
	connection, reader, writer, err := prober.openAuthenticatedIMAP(ctx, credentials)
	if err != nil {
		return nil, err
	}
	defer connection.Close()
	namespacePrefix, namespaceDelimiter := "", ""
	if err := writeIMAPCommand(writer, "D004 NAMESPACE"); err == nil {
		if lines, responseErr := readIMAPResponse(reader, "D004", false); responseErr == nil {
			namespacePrefix, namespaceDelimiter = parsePersonalNamespace(lines)
		}
	}
	if err := writeIMAPCommand(writer, `D005 LIST "" "*"`); err != nil {
		return nil, classifyConnectionError(ctx, err)
	}
	listLines, err := readIMAPResponseLimit(reader, "D005", false, maxDiscoveredFolders+2)
	if err != nil {
		if errors.Is(err, ErrProtocol) {
			return nil, ErrFolderDiscovery
		}
		return nil, classifyConnectionError(ctx, err)
	}
	if err := writeIMAPCommand(writer, `D006 LSUB "" "*"`); err != nil {
		return nil, classifyConnectionError(ctx, err)
	}
	subscribed := map[string]bool{}
	if subscriptionLines, subscriptionErr := readIMAPResponseLimit(reader, "D006", false, maxDiscoveredFolders+2); subscriptionErr == nil {
		for _, line := range subscriptionLines {
			parsed, ok := parseListResponse(line, "LSUB")
			if strings.HasPrefix(strings.ToUpper(line), "* LSUB ") && !ok {
				return nil, ErrFolderDiscovery
			}
			if ok {
				subscribed[parsed.WireName] = true
			}
		}
	}
	folders := make([]DiscoveredFolder, 0, len(listLines))
	for _, line := range listLines {
		folder, ok := parseListResponse(line, "LIST")
		if !ok {
			if strings.HasPrefix(strings.ToUpper(line), "* LIST ") {
				return nil, ErrFolderDiscovery
			}
			continue
		}
		if len(folders) >= maxDiscoveredFolders {
			return nil, ErrFolderLimit
		}
		if folder.Delimiter == "" {
			folder.Delimiter = namespaceDelimiter
		}
		folder.NamespacePrefix = namespacePrefix
		folder.Subscribed = folder.Subscribed || subscribed[folder.WireName]
		if folder.Selectable {
			tag := fmt.Sprintf("S%04d", len(folders)+1)
			if err := writeIMAPCommand(writer, tag+" STATUS "+quoteIMAP(folder.WireName)+" (MESSAGES UNSEEN UIDNEXT UIDVALIDITY)"); err != nil {
				return nil, classifyConnectionError(ctx, err)
			}
			statusLines, statusErr := readIMAPResponse(reader, tag, false)
			if statusErr != nil {
				if errors.Is(statusErr, ErrProtocol) {
					return nil, ErrFolderDiscovery
				}
				return nil, classifyConnectionError(ctx, statusErr)
			}
			if err := applyStatus(statusLines, &folder); err != nil {
				return nil, err
			}
		}
		folders = append(folders, folder)
	}
	_ = writeIMAPCommand(writer, "D999 LOGOUT")
	return folders, nil
}

func (prober *NetworkProber) openAuthenticatedIMAP(ctx context.Context, credentials storedCredentials) (net.Conn, *bufio.Reader, *bufio.Writer, error) {
	connection, err := prober.dial(ctx, credentials.IMAP)
	if err != nil {
		return nil, nil, nil, classifyConnectionError(ctx, err)
	}
	fail := func(err error) (net.Conn, *bufio.Reader, *bufio.Writer, error) {
		_ = connection.Close()
		return nil, nil, nil, err
	}
	reader := bufio.NewReaderSize(connection, 64<<10)
	writer := bufio.NewWriterSize(connection, 8<<10)
	if credentials.IMAP.TLSMode == TLSStartTLS {
		if err := expectIMAPGreeting(reader); err != nil {
			return fail(err)
		}
		if err := writeIMAPCommand(writer, "D001 STARTTLS"); err != nil {
			return fail(classifyConnectionError(ctx, err))
		}
		if _, err := readIMAPResponse(reader, "D001", false); err != nil {
			if errors.Is(err, ErrProtocol) {
				return fail(ErrCapability)
			}
			return fail(classifyConnectionError(ctx, err))
		}
		tlsConnection := tls.Client(connection, prober.tlsConfig(credentials.IMAP.Host))
		if err := tlsConnection.HandshakeContext(ctx); err != nil {
			return fail(classifyConnectionError(ctx, err))
		}
		connection = tlsConnection
		reader = bufio.NewReaderSize(connection, 64<<10)
		writer = bufio.NewWriterSize(connection, 8<<10)
	} else if err := expectIMAPGreeting(reader); err != nil {
		return fail(err)
	}
	if err := writeIMAPCommand(writer, "D002 LOGIN "+quoteIMAP(credentials.Username)+" "+quoteIMAP(credentials.Password)); err != nil {
		return fail(classifyConnectionError(ctx, err))
	}
	if _, err := readIMAPResponse(reader, "D002", true); err != nil {
		return fail(err)
	}
	return connection, reader, writer, nil
}

func parseListResponse(line, responseName string) (DiscoveredFolder, bool) {
	prefix := "* " + responseName + " "
	if !strings.HasPrefix(strings.ToUpper(line), prefix) {
		return DiscoveredFolder{}, false
	}
	remainder := strings.TrimSpace(line[len(prefix):])
	if !strings.HasPrefix(remainder, "(") {
		return DiscoveredFolder{}, false
	}
	closing := strings.Index(remainder, ")")
	if closing < 0 {
		return DiscoveredFolder{}, false
	}
	attributes := strings.Fields(remainder[1:closing])
	remainder = strings.TrimSpace(remainder[closing+1:])
	delimiter, remainder, ok := consumeIMAPToken(remainder)
	if !ok {
		return DiscoveredFolder{}, false
	}
	wireName, _, ok := consumeIMAPToken(strings.TrimSpace(remainder))
	if !ok || wireName == "" {
		return DiscoveredFolder{}, false
	}
	if strings.EqualFold(delimiter, "NIL") {
		delimiter = ""
	}
	folder := DiscoveredFolder{WireName: wireName, Name: decodeModifiedUTF7(wireName), Delimiter: delimiter, Attributes: attributes, Selectable: true}
	for _, attribute := range attributes {
		switch strings.ToLower(attribute) {
		case "\\noselect":
			folder.Selectable = false
		case "\\subscribed":
			folder.Subscribed = true
		}
	}
	folder.Role = folderRole(folder.WireName, attributes)
	if hasConflictingFolderRoles(folder.WireName, attributes) {
		return DiscoveredFolder{}, false
	}
	return folder, true
}

func parsePersonalNamespace(lines []string) (string, string) {
	for _, line := range lines {
		if !strings.HasPrefix(strings.ToUpper(line), "* NAMESPACE ") {
			continue
		}
		remainder := strings.TrimSpace(line[len("* NAMESPACE "):])
		if strings.HasPrefix(strings.ToUpper(remainder), "NIL") {
			return "", ""
		}
		opening := strings.Index(remainder, "((")
		if opening < 0 {
			continue
		}
		prefix, rest, ok := consumeIMAPToken(strings.TrimSpace(remainder[opening+2:]))
		if !ok {
			continue
		}
		delimiter, _, ok := consumeIMAPToken(strings.TrimSpace(rest))
		if !ok || strings.EqualFold(delimiter, "NIL") {
			delimiter = ""
		}
		return prefix, delimiter
	}
	return "", ""
}

func consumeIMAPToken(input string) (string, string, bool) {
	input = strings.TrimSpace(input)
	if input == "" || strings.HasPrefix(input, "{") {
		return "", input, false
	}
	if input[0] != '"' {
		end := strings.IndexByte(input, ' ')
		if end < 0 {
			return input, "", true
		}
		return input[:end], input[end+1:], true
	}
	var builder strings.Builder
	escaped := false
	for index := 1; index < len(input); index++ {
		character := input[index]
		if escaped {
			builder.WriteByte(character)
			escaped = false
			continue
		}
		if character == '\\' {
			escaped = true
			continue
		}
		if character == '"' {
			return builder.String(), input[index+1:], true
		}
		builder.WriteByte(character)
	}
	return "", input, false
}

func applyStatus(lines []string, folder *DiscoveredFolder) error {
	for _, line := range lines {
		if !strings.HasPrefix(strings.ToUpper(line), "* STATUS ") {
			continue
		}
		opening := strings.LastIndex(line, "(")
		closing := strings.LastIndex(line, ")")
		if opening < 0 || closing <= opening {
			continue
		}
		fields := strings.Fields(line[opening+1 : closing])
		if len(fields)%2 != 0 {
			return ErrFolderDiscovery
		}
		values := map[string]int64{}
		for index := 0; index < len(fields); index += 2 {
			value, err := strconv.ParseInt(fields[index+1], 10, 64)
			if err != nil || value < 0 {
				return ErrFolderDiscovery
			}
			values[strings.ToUpper(fields[index])] = value
		}
		uidNext, uidNextOK := values["UIDNEXT"]
		uidValidity, uidValidityOK := values["UIDVALIDITY"]
		if !uidNextOK || !uidValidityOK || uidNext < 1 || uidValidity < 1 || values["MESSAGES"] > math.MaxInt32 || values["UNSEEN"] > math.MaxInt32 {
			return ErrFolderDiscovery
		}
		folder.UIDNext, folder.UIDValidity = &uidNext, &uidValidity
		folder.TotalCount, folder.UnreadCount = int32(values["MESSAGES"]), int32(values["UNSEEN"])
		if folder.UnreadCount > folder.TotalCount {
			return ErrFolderDiscovery
		}
		return nil
	}
	return ErrFolderDiscovery
}

func folderRole(wireName string, attributes []string) mail.MailboxRole {
	if strings.EqualFold(wireName, "INBOX") {
		return mail.MailboxInbox
	}
	roles := map[string]mail.MailboxRole{"\\inbox": mail.MailboxInbox, "\\sent": mail.MailboxSent, "\\drafts": mail.MailboxDrafts, "\\trash": mail.MailboxTrash, "\\junk": mail.MailboxJunk, "\\archive": mail.MailboxArchive, "\\all": mail.MailboxAll}
	var role mail.MailboxRole
	for _, attribute := range attributes {
		if candidate := roles[strings.ToLower(attribute)]; candidate != "" {
			if role != "" && role != candidate {
				return ""
			}
			role = candidate
		}
	}
	return role
}

func hasConflictingFolderRoles(wireName string, attributes []string) bool {
	roles := map[mail.MailboxRole]bool{}
	if strings.EqualFold(wireName, "INBOX") {
		roles[mail.MailboxInbox] = true
	}
	known := map[string]mail.MailboxRole{"\\inbox": mail.MailboxInbox, "\\sent": mail.MailboxSent, "\\drafts": mail.MailboxDrafts, "\\trash": mail.MailboxTrash, "\\junk": mail.MailboxJunk, "\\archive": mail.MailboxArchive, "\\all": mail.MailboxAll}
	for _, attribute := range attributes {
		if role := known[strings.ToLower(attribute)]; role != "" {
			roles[role] = true
		}
	}
	return len(roles) > 1
}

func decodeModifiedUTF7(value string) string {
	if !strings.Contains(value, "&") {
		return value
	}
	var result strings.Builder
	for len(value) > 0 {
		start := strings.IndexByte(value, '&')
		if start < 0 {
			result.WriteString(value)
			break
		}
		result.WriteString(value[:start])
		value = value[start+1:]
		end := strings.IndexByte(value, '-')
		if end < 0 {
			return ""
		}
		encoded := value[:end]
		value = value[end+1:]
		if encoded == "" {
			result.WriteByte('&')
			continue
		}
		encoded = strings.ReplaceAll(encoded, ",", "/")
		encoded += strings.Repeat("=", (4-len(encoded)%4)%4)
		bytes, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil || len(bytes)%2 != 0 {
			return ""
		}
		units := make([]uint16, len(bytes)/2)
		for index := range units {
			units[index] = uint16(bytes[index*2])<<8 | uint16(bytes[index*2+1])
		}
		decoded := string(utf16.Decode(units))
		if !utf8.ValidString(decoded) {
			return ""
		}
		result.WriteString(decoded)
	}
	return result.String()
}

var _ FolderDiscoverer = (*NetworkProber)(nil)
